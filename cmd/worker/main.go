// Command worker consumes summary and roast jobs from SQS: Telegram download -> Transcribe -> Bedrock LLM -> reply.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"

	"github.com/vitkarpov/tldr/internal/config"
	"github.com/vitkarpov/tldr/internal/job"
	"github.com/vitkarpov/tldr/internal/store"
	"github.com/vitkarpov/tldr/internal/summarize"
	"github.com/vitkarpov/tldr/internal/telegram"
	"github.com/vitkarpov/tldr/internal/texts"
	"github.com/vitkarpov/tldr/internal/transcribe"
)

type app struct {
	cfg        *config.Config
	tg         *telegram.Client
	store      *store.Store
	tr         *transcribe.Transcriber
	sum        *summarize.Summarizer
	maxReceive int
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		fatal("config", err)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		fatal("aws config", err)
	}
	token, err := config.BotToken(ctx, awsCfg, cfg.BotTokenParam)
	if err != nil {
		fatal("bot token", err)
	}
	sum := summarize.New(awsCfg, summarize.Options{
		Model:           cfg.Model,
		RoastModel:      cfg.RoastModel,
		Temperature:     cfg.Temperature,
		SummaryLanguage: cfg.SummaryLanguage,
	})
	maxReceive, _ := strconv.Atoi(os.Getenv("MAX_RECEIVE_COUNT"))
	a := &app{
		cfg:        cfg,
		tg:         telegram.New(token),
		store:      store.New(awsCfg, cfg.TableName),
		tr:         transcribe.New(awsCfg, cfg.BucketName, cfg.LanguageOptions),
		sum:        sum,
		maxReceive: max(maxReceive, 1),
	}
	lambda.Start(a.handle)
}

func fatal(msg string, err error) {
	slog.Error(msg, "err", err)
	os.Exit(1)
}

// handle processes a batch (configured as size 1). Returning an error lets SQS redeliver;
// after MAX_RECEIVE_COUNT attempts the message goes to the DLQ.
func (a *app) handle(ctx context.Context, ev events.SQSEvent) error {
	for _, rec := range ev.Records {
		var j job.Job
		if err := json.Unmarshal([]byte(rec.Body), &j); err != nil {
			slog.Error("bad job, dropping", "err", err)
			continue
		}
		attempt, _ := strconv.Atoi(rec.Attributes["ApproximateReceiveCount"])
		err := a.process(ctx, j)
		if err == nil {
			continue
		}
		slog.Error("job failed", "chat_id", j.ChatID, "attempt", attempt, "err", err)
		if attempt >= a.maxReceive {
			_ = a.tg.Reply(context.Background(), j.ChatID, j.CommandMsgID, texts.Failed)
			return nil // don't redeliver: the user's already been told
		}
		return err
	}
	return nil
}

func (a *app) process(ctx context.Context, j job.Job) error {
	// Defense in depth: the webhook already checked, but never spend money on a non-whitelisted chat.
	if !a.cfg.Allowed(j.ChatID) {
		slog.Warn("worker: chat not whitelisted, dropping", "chat_id", j.ChatID)
		return nil
	}
	if j.DurationSec > a.cfg.MaxDurationSec {
		return nil
	}
	_ = a.tg.SendTyping(ctx, j.ChatID)
	if j.Mode == job.ModeRoast {
		return a.roast(ctx, j)
	}

	tr, err := a.transcript(ctx, j)
	if err != nil {
		return err
	}
	if len([]rune(tr.Text)) < 3 {
		return a.tg.Reply(ctx, j.ChatID, j.MediaMsgID, texts.Silence)
	}

	_ = a.tg.SendTyping(ctx, j.ChatID)
	summary, err := a.sum.Summarize(ctx, summarize.Input{
		Transcript:    tr.Text,
		Language:      tr.Language,
		SpeakerName:   j.SpeakerName,
		RequesterName: j.RequesterName,
		Kind:          j.Kind,
		DurationSec:   j.DurationSec,
		StyleHint:     j.StyleHint,
		PostedAt:      unixTime(j.PostedAt),
	})
	if errors.Is(err, summarize.ErrRefused) {
		return a.tg.Reply(ctx, j.ChatID, j.MediaMsgID, texts.Refused)
	}
	if err != nil {
		return err
	}
	return a.tg.Reply(ctx, j.ChatID, j.MediaMsgID, summary)
}

func (a *app) roast(ctx context.Context, j job.Job) error {
	in := summarize.RoastInput{
		Text:          j.Text,
		ForwardedFrom: j.ForwardedFrom,
		Context:       j.StyleHint,
		PostedAt:      unixTime(j.PostedAt),
	}
	if j.FileID != "" {
		tr, err := a.transcript(ctx, j)
		if err != nil {
			return err
		}
		in.Transcript = tr.Text
	}
	if j.PhotoFileID != "" {
		img, err := a.tg.DownloadFile(ctx, j.PhotoFileID)
		if err != nil {
			return fmt.Errorf("download photo: %w", err)
		}
		if format, ok := imageFormat(img); ok && len(img) <= telegram.MaxImageBytes {
			in.Image, in.ImageFormat = img, format
		} else {
			slog.Warn("roast: skipping unsupported image", "chat_id", j.ChatID, "bytes", len(img))
		}
	}
	if len([]rune(strings.TrimSpace(in.Text+in.Transcript))) < 3 && in.Image == nil {
		return a.tg.Reply(ctx, j.ChatID, j.MediaMsgID, texts.Silence)
	}

	_ = a.tg.SendTyping(ctx, j.ChatID)
	roast, err := a.sum.Roast(ctx, in)
	if errors.Is(err, summarize.ErrRefused) {
		return a.tg.Reply(ctx, j.ChatID, j.MediaMsgID, texts.Refused)
	}
	if err != nil {
		return err
	}
	return a.tg.Reply(ctx, j.ChatID, j.MediaMsgID, roast)
}

func unixTime(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func imageFormat(b []byte) (types.ImageFormat, bool) {
	switch http.DetectContentType(b) {
	case "image/jpeg":
		return types.ImageFormatJpeg, true
	case "image/png":
		return types.ImageFormatPng, true
	case "image/webp":
		return types.ImageFormatWebp, true
	case "image/gif":
		return types.ImageFormatGif, true
	}
	return "", false
}

// transcript returns a cached transcript for the file or produces (and caches) a new one,
// so asking for a second summary of the same message doesn't pay for Transcribe again.
func (a *app) transcript(ctx context.Context, j job.Job) (*store.Transcript, error) {
	if cached, err := a.store.GetTranscript(ctx, j.FileUniqueID); err != nil {
		slog.Warn("transcript cache read", "err", err)
	} else if cached != nil {
		slog.Info("transcript cache hit", "chat_id", j.ChatID)
		return cached, nil
	}

	format, err := transcribe.MediaFormat(j.Kind, j.MimeType)
	if err != nil {
		return nil, err
	}
	data, err := a.tg.DownloadFile(ctx, j.FileID)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	res, err := a.tr.Transcribe(ctx, j.FileUniqueID, data, format)
	if err != nil {
		return nil, err
	}
	t := &store.Transcript{Text: res.Text, Language: res.Language}
	if err := a.store.PutTranscript(ctx, j.FileUniqueID, *t, time.Duration(a.cfg.CacheTTLHours)*time.Hour); err != nil {
		slog.Warn("transcript cache write", "err", err)
	}
	return t, nil
}
