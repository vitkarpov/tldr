// Package transcribe turns an audio/video file into text using Amazon Transcribe batch jobs.
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/transcribe"
	"github.com/aws/aws-sdk-go-v2/service/transcribe/types"

	"github.com/vitkarpov/tldr/internal/telegram"
)

type Transcriber struct {
	s3              *s3.Client
	tr              *transcribe.Client
	bucket          string
	languageOptions []string
	pollInterval    time.Duration
}

func New(cfg aws.Config, bucket string, languageOptions []string) *Transcriber {
	return &Transcriber{
		s3:              s3.NewFromConfig(cfg),
		tr:              transcribe.NewFromConfig(cfg),
		bucket:          bucket,
		languageOptions: languageOptions,
		pollInterval:    2 * time.Second,
	}
}

type Result struct {
	Text     string
	Language string
}

// MediaFormat maps a Telegram media kind + MIME type to a Transcribe media format.
func MediaFormat(kind, mime string) (types.MediaFormat, error) {
	switch kind {
	case telegram.KindVoice:
		return types.MediaFormatOgg, nil
	case telegram.KindVideoNote:
		return types.MediaFormatMp4, nil
	}
	switch strings.ToLower(mime) {
	case "audio/ogg", "audio/opus", "video/ogg":
		return types.MediaFormatOgg, nil
	case "audio/mpeg", "audio/mp3":
		return types.MediaFormatMp3, nil
	case "audio/wav", "audio/x-wav", "audio/wave":
		return types.MediaFormatWav, nil
	case "audio/flac", "audio/x-flac":
		return types.MediaFormatFlac, nil
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac":
		return types.MediaFormatM4a, nil
	case "audio/amr":
		return types.MediaFormatAmr, nil
	case "audio/webm", "video/webm":
		return types.MediaFormatWebm, nil
	case "video/mp4", "video/quicktime", "":
		return types.MediaFormatMp4, nil
	}
	return "", fmt.Errorf("unsupported media type %q", mime)
}

// Transcribe uploads data to S3, runs a Transcribe job, waits for it and cleans up after itself.
func (t *Transcriber) Transcribe(ctx context.Context, id string, data []byte, format types.MediaFormat) (*Result, error) {
	jobName := fmt.Sprintf("tg-%s-%d", sanitize(id), time.Now().UnixNano())
	inKey := "input/" + jobName + "." + string(format)
	outKey := "output/" + jobName + ".json"

	if _, err := t.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(t.bucket),
		Key:    aws.String(inKey),
		Body:   bytes.NewReader(data),
	}); err != nil {
		return nil, fmt.Errorf("upload media: %w", err)
	}
	// Best-effort cleanup; the bucket lifecycle rule is the safety net.
	defer t.cleanup(jobName, inKey, outKey)

	in := &transcribe.StartTranscriptionJobInput{
		TranscriptionJobName: aws.String(jobName),
		Media:                &types.Media{MediaFileUri: aws.String(fmt.Sprintf("s3://%s/%s", t.bucket, inKey))},
		MediaFormat:          format,
		OutputBucketName:     aws.String(t.bucket),
		OutputKey:            aws.String(outKey),
	}
	switch len(t.languageOptions) {
	case 0:
		in.IdentifyLanguage = aws.Bool(true)
	case 1:
		in.LanguageCode = types.LanguageCode(t.languageOptions[0])
	default:
		in.IdentifyLanguage = aws.Bool(true)
		for _, l := range t.languageOptions {
			in.LanguageOptions = append(in.LanguageOptions, types.LanguageCode(l))
		}
	}
	if _, err := t.tr.StartTranscriptionJob(ctx, in); err != nil {
		return nil, fmt.Errorf("start transcription: %w", err)
	}

	var lang string
	for {
		out, err := t.tr.GetTranscriptionJob(ctx, &transcribe.GetTranscriptionJobInput{TranscriptionJobName: aws.String(jobName)})
		if err != nil {
			return nil, fmt.Errorf("get transcription job: %w", err)
		}
		j := out.TranscriptionJob
		if j.TranscriptionJobStatus == types.TranscriptionJobStatusCompleted {
			lang = string(j.LanguageCode)
			break
		}
		if j.TranscriptionJobStatus == types.TranscriptionJobStatusFailed {
			return nil, fmt.Errorf("transcription failed: %s", aws.ToString(j.FailureReason))
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("transcription timed out: %w", ctx.Err())
		case <-time.After(t.pollInterval):
		}
	}

	obj, err := t.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(t.bucket), Key: aws.String(outKey)})
	if err != nil {
		return nil, fmt.Errorf("read transcript: %w", err)
	}
	defer obj.Body.Close()
	var doc struct {
		Results struct {
			Transcripts []struct {
				Transcript string `json:"transcript"`
			} `json:"transcripts"`
		} `json:"results"`
	}
	if err := json.NewDecoder(obj.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("decode transcript: %w", err)
	}
	var sb strings.Builder
	for _, tr := range doc.Results.Transcripts {
		sb.WriteString(tr.Transcript)
	}
	return &Result{Text: strings.TrimSpace(sb.String()), Language: lang}, nil
}

func (t *Transcriber) cleanup(jobName string, keys ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, k := range keys {
		if _, err := t.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(t.bucket), Key: aws.String(k)}); err != nil {
			slog.Warn("cleanup: delete object", "key", k, "err", err)
		}
	}
	if _, err := t.tr.DeleteTranscriptionJob(ctx, &transcribe.DeleteTranscriptionJobInput{TranscriptionJobName: aws.String(jobName)}); err != nil {
		slog.Warn("cleanup: delete transcription job", "job", jobName, "err", err)
	}
}

// sanitize keeps job names within Transcribe's allowed charset ([0-9a-zA-Z._-]).
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 60 {
		return b.String()[:60]
	}
	return b.String()
}
