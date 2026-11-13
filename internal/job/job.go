// Package job defines the message passed from the webhook to the worker over SQS.
package job

type Job struct {
	ChatID        int64  `json:"chat_id"`
	CommandMsgID  int64  `json:"command_msg_id"`
	MediaMsgID    int64  `json:"media_msg_id"`
	Kind          string `json:"kind"`
	FileID        string `json:"file_id"`
	FileUniqueID  string `json:"file_unique_id"`
	MimeType      string `json:"mime_type"`
	DurationSec   int    `json:"duration_sec"`
	SpeakerName   string `json:"speaker_name"`
	RequesterName string `json:"requester_name"`
	StyleHint     string `json:"style_hint,omitempty"`
}
