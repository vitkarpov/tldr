// Package texts holds every message users can see in the chat (in Russian).
// Logs stay in English.
package texts

import "fmt"

const (
	Help = "Привет! Ответь на голосовое или кружочек командой /summary (или /tldr) — и я расскажу, что там, с шутками и без потерь.\n" +
		"Можно заказать стиль: /summary как спортивный комментатор"

	ReplyToMedia = "Мне нечего пересказывать — команду надо отправить ответом на голосовое или видео. 🎧\n\n" +
		"Как это сделать:\n" +
		"1. Свайпни голосовое влево (или зажми его → «Ответить»)\n" +
		"2. Напиши /tldr и отправь\n\n" +
		"Можно заказать стиль: /tldr как спортивный комментатор"

	TooLargeFile  = "Файл больше 20 МБ — Telegram не даёт ботам такое скачивать. 😢"
	QuotaExceeded = "На сегодня лимит саммари в этом чате исчерпан. 🥵 Приходите завтра или (о ужас) послушайте сами."
	Silence       = "Я внимательно послушал и услышал… ничего. Чистая, прекрасная тишина. 🦗"
	Refused       = "Это я, пожалуй, пропущу. 🙊"
	NotAllowed    = "Я тут работаю только по приглашению. 🔒 Хотите пользоваться ботом — напишите @vitkarpov в Telegram."
	Failed        = "Я пытался, честно, но что-то сломалось, пока я слушал. 🫠 Попробуйте чуть позже."
)

func ChatID(id int64) string {
	return fmt.Sprintf("ID этого чата: %d", id)
}

func TooLong(durationSec, maxSec int) string {
	m := durationSec / 60
	return fmt.Sprintf("%d %s?! Это уже не голосовое, а подкаст. 🎙️ Мой предел — %d %s.",
		m, Minutes(m), maxSec/60, Minutes(maxSec/60))
}

// Minutes returns the correct Russian plural form of "минута" for n.
func Minutes(n int) string {
	return plural(n, "минута", "минуты", "минут")
}

func plural(n int, one, few, many string) string {
	if n < 0 {
		n = -n
	}
	switch {
	case n%10 == 1 && n%100 != 11:
		return one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return few
	default:
		return many
	}
}
