// Command webhook receives Telegram updates via API Gateway, validates them and enqueues jobs.
// It must answer fast (Telegram retries slow webhooks), so the heavy lifting happens in the worker.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/vitkarpov/tldr/internal/config"
	"github.com/vitkarpov/tldr/internal/job"
	"github.com/vitkarpov/tldr/internal/store"
	"github.com/vitkarpov/tldr/internal/telegram"
	"github.com/vitkarpov/tldr/internal/texts"
)

const maxStyleHintLen = 200

type app struct {
	cfg         *config.Config
	tg          *telegram.Client
	botUsername string
	sqs         *sqs.Client
	store       *store.Store
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("aws config", "err", err)
		os.Exit(1)
	}
	token, err := config.BotToken(ctx, awsCfg, cfg.BotTokenParam)
	if err != nil {
		slog.Error("bot token", "err", err)
		os.Exit(1)
	}
	tg := telegram.New(token)
	me, err := tg.GetMe(ctx)
	if err != nil {
		slog.Error("getMe", "err", err)
		os.Exit(1)
	}
	a := &app{cfg: cfg, tg: tg, botUsername: me.Username, sqs: sqs.NewFromConfig(awsCfg), store: store.New(awsCfg, cfg.TableName)}
	lambda.Start(a.handle)
}

func (a *app) handle(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	ok := events.APIGatewayV2HTTPResponse{StatusCode: 200}
	secret := req.Headers["x-telegram-bot-api-secret-token"]
	if a.cfg.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(a.cfg.WebhookSecret)) != 1 {
		return events.APIGatewayV2HTTPResponse{StatusCode: 401}, nil
	}
	var upd telegram.Update
	if err := json.Unmarshal([]byte(req.Body), &upd); err != nil {
		slog.Warn("bad update body", "err", err)
		return ok, nil
	}
	if upd.Message == nil {
		return ok, nil
	}
	// Always answer 200: an error here makes Telegram redeliver the same update over and over.
	if err := a.handleMessage(ctx, upd.Message); err != nil {
		slog.Error("handle message", "chat_id", upd.Message.Chat.ID, "err", err)
	}
	return ok, nil
}

func (a *app) handleMessage(ctx context.Context, m *telegram.Message) error {
	cmd, args := a.parseCommand(m)
	chatID := m.Chat.ID

	// /chatid works everywhere so you can discover IDs for the whitelist. It costs nothing.
	if cmd == "chatid" {
		return a.tg.Reply(ctx, chatID, m.MessageID, texts.ChatID(chatID))
	}

	if !a.cfg.Allowed(chatID) {
		// Reply only to things aimed at the bot (commands, or media/commands in a DM). Telegram
		// calls are free, so this can't burn money; it just tells people whom to ask.
		_, media := m.ExtractMedia()
		if cmd != "" || (m.Chat.Type == "private" && media != nil) {
			slog.Warn("chat not whitelisted", "chat_id", chatID, "chat_type", m.Chat.Type, "chat_title", m.Chat.Title)
			return a.tg.Reply(ctx, chatID, m.MessageID, texts.NotAllowed)
		}
		return nil
	}

	var target *telegram.Message
	switch {
	case cmd == "summary" || cmd == "tldr" || cmd == "s":
		target = m.ReplyToMessage
		if k, _ := target.ExtractMedia(); k == "" {
			return a.tg.Reply(ctx, chatID, m.MessageID, texts.ReplyToMedia)
		}
	case cmd == "start" || cmd == "help":
		return a.tg.Reply(ctx, chatID, m.MessageID, texts.Help)
	case cmd == "" && m.Chat.Type == "private":
		// In a DM, just summarize whatever voice/video you send or forward.
		if k, _ := m.ExtractMedia(); k != "" {
			target = m
		}
	}
	if target == nil {
		return nil
	}

	kind, media := target.ExtractMedia()
	if media.Duration > a.cfg.MaxDurationSec {
		return a.tg.Reply(ctx, chatID, m.MessageID, texts.TooLong(media.Duration, a.cfg.MaxDurationSec))
	}
	if media.FileSize > telegram.MaxDownloadBytes {
		return a.tg.Reply(ctx, chatID, m.MessageID, texts.TooLargeFile)
	}

	if err := a.store.TakeQuota(ctx, chatID, a.cfg.DailyLimit); err != nil {
		if errors.Is(err, store.ErrQuotaExceeded) {
			return a.tg.Reply(ctx, chatID, m.MessageID, texts.QuotaExceeded)
		}
		return fmt.Errorf("quota: %w", err)
	}

	if len([]rune(args)) > maxStyleHintLen {
		args = string([]rune(args)[:maxStyleHintLen])
	}
	j := job.Job{
		ChatID:        chatID,
		CommandMsgID:  m.MessageID,
		MediaMsgID:    target.MessageID,
		Kind:          kind,
		FileID:        media.FileID,
		FileUniqueID:  media.FileUniqueID,
		MimeType:      media.MimeType,
		DurationSec:   media.Duration,
		SpeakerName:   target.From.DisplayName(),
		RequesterName: m.From.DisplayName(),
		StyleHint:     args,
	}
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if _, err := a.sqs.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(a.cfg.QueueURL), MessageBody: aws.String(string(body))}); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	_ = a.tg.SendTyping(ctx, chatID)
	slog.Info("job enqueued", "chat_id", chatID, "kind", kind, "duration", media.Duration)
	return nil
}

// parseCommand returns ("summary", "like a pirate") for "/summary@MyBot like a pirate".
// Commands addressed to a different bot are ignored.
func (a *app) parseCommand(m *telegram.Message) (cmd, args string) {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	first, rest, _ := strings.Cut(text, " ")
	name, target, hasTarget := strings.Cut(strings.TrimPrefix(first, "/"), "@")
	if hasTarget && !strings.EqualFold(target, a.botUsername) {
		return "", ""
	}
	return strings.ToLower(name), strings.TrimSpace(rest)
}
