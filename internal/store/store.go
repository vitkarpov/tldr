// Package store keeps per-chat daily quotas and a transcript cache in DynamoDB.
package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type Store struct {
	db    *dynamodb.Client
	table string
}

func New(cfg aws.Config, table string) *Store {
	return &Store{db: dynamodb.NewFromConfig(cfg), table: table}
}

var ErrQuotaExceeded = errors.New("daily quota exceeded")

// TakeQuota atomically increments today's (UTC) counter for chatID, failing once limit is reached.
func (s *Store) TakeQuota(ctx context.Context, chatID int64, limit int) error {
	now := time.Now().UTC()
	pk := fmt.Sprintf("quota#%d#%s", chatID, now.Format("2006-01-02"))
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                aws.String(s.table),
		Key:                      map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}},
		UpdateExpression:         aws.String("ADD #c :one SET expires_at = :ttl"),
		ConditionExpression:      aws.String("attribute_not_exists(#c) OR #c < :max"),
		ExpressionAttributeNames: map[string]string{"#c": "count"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":one": &types.AttributeValueMemberN{Value: "1"},
			":max": &types.AttributeValueMemberN{Value: strconv.Itoa(limit)},
			":ttl": &types.AttributeValueMemberN{Value: strconv.FormatInt(now.Add(48*time.Hour).Unix(), 10)},
		},
	})
	var ccf *types.ConditionalCheckFailedException
	if errors.As(err, &ccf) {
		return ErrQuotaExceeded
	}
	return err
}

type Transcript struct {
	Text     string
	Language string
}

// GetTranscript returns a cached transcript, or nil if there is none.
func (s *Store) GetTranscript(ctx context.Context, fileUniqueID string) (*Transcript, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table),
		Key:       map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "tr#" + fileUniqueID}},
	})
	if err != nil || out.Item == nil {
		return nil, err
	}
	// DynamoDB TTL deletion is lazy; respect expiry ourselves.
	if exp, ok := out.Item["expires_at"].(*types.AttributeValueMemberN); ok {
		if ts, _ := strconv.ParseInt(exp.Value, 10, 64); ts > 0 && time.Now().Unix() > ts {
			return nil, nil
		}
	}
	t := &Transcript{}
	if v, ok := out.Item["text"].(*types.AttributeValueMemberS); ok {
		t.Text = v.Value
	}
	if v, ok := out.Item["lang"].(*types.AttributeValueMemberS); ok {
		t.Language = v.Value
	}
	return t, nil
}

func (s *Store) PutTranscript(ctx context.Context, fileUniqueID string, t Transcript, ttl time.Duration) error {
	_, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table),
		Item: map[string]types.AttributeValue{
			"pk":         &types.AttributeValueMemberS{Value: "tr#" + fileUniqueID},
			"text":       &types.AttributeValueMemberS{Value: t.Text},
			"lang":       &types.AttributeValueMemberS{Value: t.Language},
			"expires_at": &types.AttributeValueMemberN{Value: strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)},
		},
	})
	return err
}
