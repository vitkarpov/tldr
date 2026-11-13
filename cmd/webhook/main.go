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

// errQuotaAnswered means the user was already told the quota is spent; nothing to log.
var errQuotaAnswered = errors.New("quota exceeded, user notified")

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
	if err := a.handleMessage(ctx, upd.Message); err != nil && !errors.Is(err, errQuotaAnswered) {
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
	case cmd == "roast" || cmd == "r":
		return a.handleRoast(ctx, m, args)
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
	if ok, err := a.checkMedia(ctx, m, media); !ok {
		return err
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
		StyleHint:     truncate(args, maxStyleHintLen),
		PostedAt:      target.PostedAt(),
	}
	return a.enqueue(ctx, j)
}

// handleRoast queues a roast of the replied-to post: text, caption, photo and/or voice/video.
func (a *app) handleRoast(ctx context.Context, m *telegram.Message, args string) error {
	target := m.ReplyToMessage
	text := target.TextContent()
	photo := target.BestPhoto()
	kind, media := target.ExtractMedia()
	if target == nil || (strings.TrimSpace(text) == "" && photo == nil && media == nil) {
		return a.tg.Reply(ctx, m.Chat.ID, m.MessageID, texts.ReplyToRoast)
	}
	if media != nil {
		if ok, err := a.checkMedia(ctx, m, media); !ok {
			return err
		}
	} else if err := a.takeQuota(ctx, m); err != nil {
		return err
	}
	j := job.Job{
		Mode:          job.ModeRoast,
		ChatID:        m.Chat.ID,
		CommandMsgID:  m.MessageID,
		MediaMsgID:    target.MessageID,
		SpeakerName:   target.From.DisplayName(),
		RequesterName: m.From.DisplayName(),
		StyleHint:     truncate(args, maxStyleHintLen),
		Text:          truncate(text, telegram.MaxMessageLen),
		ForwardedFrom: target.ForwardOrigin.Name(),
		PostedAt:      target.PostedAt(),
	}
	if target.ForwardOrigin != nil && j.ForwardedFrom == "" {
		j.ForwardedFrom = "an unknown author"
	}
	if photo != nil {
		j.PhotoFileID = photo.FileID
	}
	if media != nil {
		j.Kind, j.FileID, j.FileUniqueID, j.MimeType, j.DurationSec = kind, media.FileID, media.FileUniqueID, media.MimeType, media.Duration
	}
	return a.enqueue(ctx, j)
}

// checkMedia enforces size/duration limits and takes quota. ok=false means the user was answered.
func (a *app) checkMedia(ctx context.Context, m *telegram.Message, media *telegram.Media) (ok bool, err error) {
	if media.Duration > a.cfg.MaxDurationSec {
		return false, a.tg.Reply(ctx, m.Chat.ID, m.MessageID, texts.TooLong(media.Duration, a.cfg.MaxDurationSec))
	}
	if media.FileSize > telegram.MaxDownloadBytes {
		return false, a.tg.Reply(ctx, m.Chat.ID, m.MessageID, texts.TooLargeFile)
	}
	if err := a.takeQuota(ctx, m); err != nil {
		return false, err
	}
	return true, nil
}

// takeQuota returns errQuotaAnswered (after telling the user) when the daily limit is spent.
func (a *app) takeQuota(ctx context.Context, m *telegram.Message) error {
	err := a.store.TakeQuota(ctx, m.Chat.ID, a.cfg.DailyLimit)
	if errors.Is(err, store.ErrQuotaExceeded) {
		if rerr := a.tg.Reply(ctx, m.Chat.ID, m.MessageID, texts.QuotaExceeded); rerr != nil {
			return rerr
		}
		return errQuotaAnswered
	}
	if err != nil {
		return fmt.Errorf("quota: %w", err)
	}
	return nil
}

func (a *app) enqueue(ctx context.Context, j job.Job) error {
	body, err := json.Marshal(j)
	if err != nil {
		return err
	}
	if _, err := a.sqs.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(a.cfg.QueueURL), MessageBody: aws.String(string(body))}); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	_ = a.tg.SendTyping(ctx, j.ChatID)
	slog.Info("job enqueued", "chat_id", j.ChatID, "mode", j.Mode, "kind", j.Kind, "duration", j.DurationSec,
		"forwarded", j.ForwardedFrom != "", "photo", j.PhotoFileID != "")
	return nil
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
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
