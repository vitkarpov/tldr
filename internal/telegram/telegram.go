// Package telegram is a tiny Bot API client covering only what this bot needs.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const apiBase = "https://api.telegram.org"

// MaxDownloadBytes is the Bot API limit for getFile downloads.
const MaxDownloadBytes = 20 * 1024 * 1024

// MaxMessageLen is the Bot API limit for a text message.
const MaxMessageLen = 4096

type Update struct {
	UpdateID int64    `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

func (u *User) DisplayName() string {
	if u == nil {
		return "someone"
	}
	if u.LastName != "" {
		return u.FirstName + " " + u.LastName
	}
	if u.FirstName != "" {
		return u.FirstName
	}
	return u.Username
}

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // private, group, supergroup, channel
	Title string `json:"title,omitempty"`
}

type Message struct {
	MessageID      int64          `json:"message_id"`
	From           *User          `json:"from,omitempty"`
	Chat           Chat           `json:"chat"`
	Text           string         `json:"text,omitempty"`
	Caption        string         `json:"caption,omitempty"`
	ReplyToMessage *Message       `json:"reply_to_message,omitempty"`
	Voice          *Media         `json:"voice,omitempty"`
	Audio          *Media         `json:"audio,omitempty"`
	VideoNote      *Media         `json:"video_note,omitempty"`
	Video          *Media         `json:"video,omitempty"`
	Photo          []PhotoSize    `json:"photo,omitempty"`
	ForwardOrigin  *MessageOrigin `json:"forward_origin,omitempty"`
	Entities       []Entity       `json:"entities,omitempty"`
}

// PhotoSize is one resolution of a photo; Telegram sends several, smallest first.
type PhotoSize struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// MessageOrigin describes where a forwarded message came from.
type MessageOrigin struct {
	Type            string `json:"type"` // user, hidden_user, chat, channel
	SenderUser      *User  `json:"sender_user,omitempty"`
	SenderUserName  string `json:"sender_user_name,omitempty"`
	Chat            *Chat  `json:"chat,omitempty"`
	SenderChat      *Chat  `json:"sender_chat,omitempty"`
	AuthorSignature string `json:"author_signature,omitempty"`
}

// Name returns a human-readable author of the forwarded message.
func (o *MessageOrigin) Name() string {
	switch {
	case o == nil:
		return ""
	case o.SenderUser != nil:
		return o.SenderUser.DisplayName()
	case o.SenderUserName != "":
		return o.SenderUserName
	}
	c := o.Chat
	if c == nil {
		c = o.SenderChat
	}
	if c == nil {
		return ""
	}
	if o.AuthorSignature != "" && o.AuthorSignature != c.Title {
		return o.AuthorSignature + " (" + c.Title + ")"
	}
	return c.Title
}

// MaxImageBytes is the Bedrock Converse limit for an inline image.
const MaxImageBytes = 3_750_000

// BestPhoto returns the largest photo size that fits the image limit, or nil.
func (m *Message) BestPhoto() *PhotoSize {
	if m == nil {
		return nil
	}
	var best *PhotoSize
	for i := range m.Photo {
		p := &m.Photo[i]
		if p.FileSize > MaxImageBytes {
			continue
		}
		if best == nil || p.Width*p.Height > best.Width*best.Height {
			best = p
		}
	}
	return best
}

// TextContent returns the message text or caption.
func (m *Message) TextContent() string {
	if m == nil {
		return ""
	}
	if m.Text != "" {
		return m.Text
	}
	return m.Caption
}

type Entity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// Media covers voice, audio, video_note and video: the fields we use are shared.
type Media struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	Duration     int    `json:"duration"`
	MimeType     string `json:"mime_type,omitempty"`
	FileSize     int64  `json:"file_size,omitempty"`
}

// MediaKind values.
const (
	KindVoice     = "voice"
	KindAudio     = "audio"
	KindVideoNote = "video_note"
	KindVideo     = "video"
)

// ExtractMedia returns the summarizable media attached to m, if any.
func (m *Message) ExtractMedia() (kind string, media *Media) {
	switch {
	case m == nil:
		return "", nil
	case m.Voice != nil:
		return KindVoice, m.Voice
	case m.VideoNote != nil:
		return KindVideoNote, m.VideoNote
	case m.Audio != nil:
		return KindAudio, m.Audio
	case m.Video != nil:
		return KindVideo, m.Video
	}
	return "", nil
}

type Client struct {
	token string
	http  *http.Client
}

func New(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/bot%s/%s", apiBase, c.token, method), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL contains the token; never surface it in logs.
		return fmt.Errorf("telegram %s: request failed", method)
	}
	defer resp.Body.Close()
	var r apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return fmt.Errorf("telegram %s: decode: %w", method, err)
	}
	if !r.OK {
		return fmt.Errorf("telegram %s: %d %s", method, r.ErrorCode, r.Description)
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", map[string]any{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// Reply sends plain text as a reply to replyTo (falls back to a normal message if it was deleted).
func (c *Client) Reply(ctx context.Context, chatID, replyTo int64, text string) error {
	if r := []rune(text); len(r) > MaxMessageLen {
		text = string(r[:MaxMessageLen-1]) + "…"
	}
	payload := map[string]any{
		"chat_id":              chatID,
		"text":                 text,
		"link_preview_options": map[string]any{"is_disabled": true},
	}
	if replyTo != 0 {
		payload["reply_parameters"] = map[string]any{
			"message_id":                  replyTo,
			"allow_sending_without_reply": true,
		}
	}
	return c.call(ctx, "sendMessage", payload, nil)
}

func (c *Client) SendTyping(ctx context.Context, chatID int64) error {
	return c.call(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": "typing"}, nil)
}

// DownloadFile resolves fileID via getFile and downloads its content.
func (c *Client) DownloadFile(ctx context.Context, fileID string) ([]byte, error) {
	var f struct {
		FilePath string `json:"file_path"`
		FileSize int64  `json:"file_size"`
	}
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &f); err != nil {
		return nil, err
	}
	if f.FileSize > MaxDownloadBytes {
		return nil, fmt.Errorf("file too large: %d bytes", f.FileSize)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/file/bot%s/%s", apiBase, c.token, f.FilePath), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram file download: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file download: status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, MaxDownloadBytes+1))
}
