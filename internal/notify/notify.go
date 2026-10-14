// Package notify sends notifications.
package notify

import (
	"context"
	"log"
)

type Notifier interface {
	Notify(ctx context.Context, title, body string) error
}

// Telegram is a stub. TODO: call https://api.telegram.org/bot<token>/sendMessage.
type Telegram struct{ BotToken, ChatID string }

func (t *Telegram) Notify(ctx context.Context, title, body string) error {
	log.Printf("[notify/telegram stub] %s: %s", title, body)
	return nil
}

// TODO: WeChat push (ServerChan/PushPlus), SMTP email.
