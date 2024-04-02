package main

import (
	"log"
	"reflect"
	"time"

	"github.com/NicoNex/echotron/v3"
	"github.com/spf13/viper"
)

type UrlExtractor func(msg *echotron.Message) []string

func GetUrlsFromEntities(msg *echotron.Message) []string {
	urls := make([]string, 0)
	for _, entity := range msg.Entities {
		if entity.Type != "url" && entity.Type != "text_link" {
			continue
		}
		url := entity.URL
		if url == "" {
			url = msg.Text[entity.Offset : entity.Offset+entity.Length]
		}
		urls = append(urls, url)
	}
	return urls
}

func GetUrlsFromLinkPreview(msg *echotron.Message) []string {
	link := msg.LinkPreviewOptions
	urls := make([]string, 0)
	if link != nil && link.URL != "" && !link.IsDisabled {
		urls = append(urls, link.URL)
	}
	return urls
}

func GetUrlsWithExtractors(extractors ...UrlExtractor) UrlExtractor {
	return func(msg *echotron.Message) []string {
		urls := make([]string, 0)
		for _, extractor := range extractors {
			urls = append(urls, extractor(msg)...)
		}
		return distinct(urls)
	}
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

type LinkdingRepository interface {
}

type linkdingRepository struct {
	url      string
	apiToken string
}

func NewLinkdingRepository(url, apiToken string) LinkdingRepository {
	return &linkdingRepository{url, apiToken}
}

type LinkService interface {
	Save(url string) error
}

type linkdingLinkService struct {
	repository LinkdingRepository
}

func NewLinkdingLinkService(repository LinkdingRepository) LinkService {
	return &linkdingLinkService{repository}
}

func (s *linkdingLinkService) Save(url string) error {
	return nil
}

type bot struct {
	chatId           int64
	allowedUsernames []string
	urlExtractor     UrlExtractor
	linkService      LinkService
	echotron.API
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

	urls := b.urlExtractor(msg)
	if len(urls) == 0 {
		b.SendMessage("No URLs found in the message", b.chatId, nil)
		return
	}

	for _, url := range urls {
		b.SendMessage(url, b.chatId, nil)
	}
}

type botFactory struct {
	tgToken          string
	allowedUsernames []string
	api              echotron.API
	urlExtractor     UrlExtractor
	linkService      LinkService
}

func NewBotFactory(
	tgToken string,
	allowedUsernames []string,
	urlExtractor UrlExtractor,
	linkService LinkService,
	api echotron.API,
) *botFactory {
	return &botFactory{
		tgToken:          tgToken,
		allowedUsernames: allowedUsernames,
		urlExtractor:     urlExtractor,
		linkService:      linkService,
		api:              api,
	}
}

func (b *botFactory) NewBotFn() echotron.NewBotFn {
	return func(chatId int64) echotron.Bot {
		return &bot{
			chatId:           chatId,
			allowedUsernames: b.allowedUsernames,
			urlExtractor:     b.urlExtractor,
			linkService:      b.linkService,
			API:              b.api,
		}
	}
}

type envConfig struct {
	Token            string   `mapstructure:"TOKEN"`
	AllowedUsernames []string `mapstructure:"ALLOWED_USERNAMES"`
}

func parseConfig(i interface{}) error {
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
	if err := parseConfig(config); err != nil {
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

	api := echotron.NewAPI(config.Token)

	res, err := api.GetMe()
	if err != nil {
		log.Fatalf("Failed to get bot info: %v", err)
	}
	log.Printf("Bot username: @%s", res.Result.Username)

	botFactory := NewBotFactory(
		config.Token,
		config.AllowedUsernames,
		GetUrlsWithExtractors(GetUrlsFromEntities, GetUrlsFromLinkPreview),
		NewLinkdingLinkService(NewLinkdingRepository("", "")), // TODO: config
		api,
	)

	dsp := echotron.NewDispatcher(config.Token, botFactory.NewBotFn())
	log.Println("Dispatcher constructed")

	for {
		log.Println("Polling...")
		log.Println(dsp.Poll())

		time.Sleep(5 * time.Second)
	}
}
