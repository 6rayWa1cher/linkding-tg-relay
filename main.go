package main

import (
	"log"
	"reflect"
	"time"

	"github.com/NicoNex/echotron/v3"
	"github.com/spf13/viper"
)

type bot struct {
	chatId           int64
	allowedUsernames []string
	echotron.API
}

func getUrlsFromEntities(msgText string, entities []*echotron.MessageEntity) []string {
	urls := make([]string, 0)
	for _, entity := range entities {
		if entity.Type != "url" && entity.Type != "text_link" {
			continue
		}
		url := entity.URL
		if url == "" {
			url = msgText[entity.Offset : entity.Offset+entity.Length]
		}
		urls = append(urls, url)
	}
	return urls
}

func getUrlsFromLinkPreview(link *echotron.LinkPreviewOptions) []string {
	urls := make([]string, 0)
	if link != nil && link.URL != "" && !link.IsDisabled {
		urls = append(urls, link.URL)
	}
	return urls
}

func getUrlsFromMessage(msg *echotron.Message) []string {
	urls := make([]string, 0)
	urls = append(urls, getUrlsFromLinkPreview(msg.LinkPreviewOptions)...)
	urls = append(urls, getUrlsFromEntities(msg.Text, msg.Entities)...)
	return distinct(urls)
}

func distinct(arr []string) []string {
	unique := make([]string, 0)
	seen := make(map[string]bool)
	for _, s := range arr {
		if !seen[s] {
			seen[s] = true
			unique = append(unique, s)
		}
	}
	return unique
}

func contains(arr []string, str string) bool {
	for _, a := range arr {
		if a == str {
			return true
		}
	}
	return false
}

func (b *bot) Update(update *echotron.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	if !contains(b.allowedUsernames, msg.From.Username) {
		b.SendMessage("You are not allowed to use this bot", b.chatId, nil)
		return
	}

	log.Printf("Received message: %v", msg)

	urls := getUrlsFromMessage(msg)
	if len(urls) == 0 {
		b.SendMessage("No URLs found in the message", b.chatId, nil)
		return
	}

	for _, url := range urls {
		b.SendMessage(url, b.chatId, nil)
	}
}

type botFactory struct {
	token            string
	allowedUsernames []string
}

func (b *botFactory) newBotFn() echotron.NewBotFn {
	api := echotron.NewAPI(b.token)
	return func(chatId int64) echotron.Bot {
		return &bot{
			chatId,
			b.allowedUsernames,
			api,
		}
	}
}

type envConfig struct {
	Token            string   `mapstructure:"TOKEN"`
	AllowedUsernames []string `mapstructure:"ALLOWED_USERNAMES"`
}

func Parse(i interface{}) error {
	r := reflect.TypeOf(i)
	for r.Kind() == reflect.Ptr {
		r = r.Elem()
	}
	for i := 0; i < r.NumField(); i++ {
		env := r.Field(i).Tag.Get("mapstructure")
		if err := viper.BindEnv(env); err != nil {
			return err
		}
	}
	return viper.Unmarshal(i)
}

func loadEnvVariables() *envConfig {
	viper.AddConfigPath(".")
	viper.SetConfigName("app")
	viper.SetConfigType("env")
	viper.SetEnvPrefix("ltr")
	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Failed to read config file: %v", err)
	}
	config := &envConfig{}
	if err := Parse(config); err != nil {
		log.Fatalf("Failed to unmarshal config: %v", err)
	}
	return config
}

func validateConfig(config *envConfig) {
	if config.Token == "" {
		log.Fatal("Token is required")
	}
	if len(config.AllowedUsernames) == 0 {
		log.Fatal("At least one allowed username is required")
	}
}

func main() {
	config := loadEnvVariables()
	validateConfig(config)
	log.Println("Config loaded successfully")
	log.Printf("Allowed usernames: %v", config.AllowedUsernames)

	botFactory := botFactory{
		token:            config.Token,
		allowedUsernames: config.AllowedUsernames,
	}

	dsp := echotron.NewDispatcher(config.Token, botFactory.newBotFn())
	log.Println("Dispatcher constructed")

	api := echotron.NewAPI(config.Token)
	res, err := api.GetMe()
	if err != nil {
		log.Fatalf("Failed to get bot info: %v", err)
	}
	log.Printf("Bot username: @%s", res.Result.Username)

	for {
		log.Println("Polling...")
		log.Println(dsp.Poll())
		// In case of connection issues wait 5 seconds before trying to reconnect.
		time.Sleep(5 * time.Second)
	}
}
