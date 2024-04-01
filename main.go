package main

import (
	"log"
	"time"

	"github.com/NicoNex/echotron/v3"
)

// Struct useful for managing internal states in your bot, but it could be of
// any type such as `type bot int64` if you only need to store the chatID.
type bot struct {
	chatID int64
	echotron.API
}

const token = "MY TELEGRAM TOKEN"

// This function needs to be of type 'echotron.NewBotFn' and is called by
// the echotron dispatcher upon any new message from a chatID that has never
// interacted with the bot before.
// This means that echotron keeps one instance of the echotron.Bot implementation
// for each chat where the bot is used.
func newBot(chatID int64) echotron.Bot {
	return &bot{
		chatID,
		echotron.NewAPI(token),
	}
}

// This method is needed to implement the echotron.Bot interface.
func (b *bot) Update(update *echotron.Update) {
	if update.Message.Text == "/start" {
		b.SendMessage("Hello world", b.chatID, nil)
	}
}

func main() {
	// This is the entry point of echotron library.
	dsp := echotron.NewDispatcher(token, newBot)
	for {
		log.Println(dsp.Poll())
		// In case of connection issues wait 5 seconds before trying to reconnect.
		time.Sleep(5 * time.Second)
	}
}
