// Package config loads settings shared by both Lambdas from the environment.
package config

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type Config struct {
	AllowedChats    map[int64]bool
	BotTokenParam   string
	TableName       string
	QueueURL        string
	BucketName      string
	WebhookSecret   string
	MaxDurationSec  int
	DailyLimit      int
	CacheTTLHours   int
	LanguageOptions []string // Transcribe language codes; empty = auto-detect from all languages
	SummaryLanguage string   // "auto" = same language as the transcript
	Model           string
	Temperature     float32
}

func Load() (*Config, error) {
	c := &Config{
		AllowedChats:    map[int64]bool{},
		BotTokenParam:   os.Getenv("BOT_TOKEN_PARAM"),
		TableName:       os.Getenv("TABLE_NAME"),
		QueueURL:        os.Getenv("QUEUE_URL"),
		BucketName:      os.Getenv("BUCKET_NAME"),
		WebhookSecret:   os.Getenv("WEBHOOK_SECRET"),
		MaxDurationSec:  envInt("MAX_DURATION_SEC", 600),
		DailyLimit:      envInt("DAILY_LIMIT_PER_CHAT", 30),
		CacheTTLHours:   envInt("TRANSCRIPT_CACHE_TTL_HOURS", 72),
		SummaryLanguage: envStr("SUMMARY_LANGUAGE", "Russian"),
		Model:           envStr("MODEL_ID", "eu.amazon.nova-lite-v1:0"),
		Temperature:     float32(envFloat("TEMPERATURE", 0.7)),
	}
	for _, s := range splitCSV(os.Getenv("ALLOWED_CHAT_IDS")) {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("ALLOWED_CHAT_IDS: bad chat id %q", s)
		}
		c.AllowedChats[id] = true
	}
	c.LanguageOptions = splitCSV(os.Getenv("TRANSCRIBE_LANGUAGE_OPTIONS"))
	if c.BotTokenParam == "" {
		return nil, fmt.Errorf("BOT_TOKEN_PARAM is required")
	}
	return c, nil
}

// Allowed fails closed: an empty whitelist allows nobody.
func (c *Config) Allowed(chatID int64) bool { return c.AllowedChats[chatID] }

// BotToken reads the Telegram token from SSM Parameter Store (SecureString).
func BotToken(ctx context.Context, awsCfg aws.Config, param string) (string, error) {
	out, err := ssm.NewFromConfig(awsCfg).GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(param),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("read bot token from SSM: %w", err)
	}
	return aws.ToString(out.Parameter.Value), nil
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func envFloat(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(k), 64); err == nil {
		return v
	}
	return def
}
