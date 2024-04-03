package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/dyatlov/go-htmlinfo/htmlinfo"
	"github.com/goware/urlx"
	"io"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"time"

	"github.com/NicoNex/echotron/v3"
	"github.com/spf13/viper"
)

const (
	ApplicationJson = "application/json"
)

type UrlExtractor func(msg *echotron.Message) []string

func GetUrlsFromEntities(msg *echotron.Message) []string {
	urls := make([]string, 0)
	for _, entity := range msg.Entities {
		if entity.Type != "url" && entity.Type != "text_link" {
			continue
		}
		entityUrl := entity.URL
		if entityUrl == "" {
			entityUrl = msg.Text[entity.Offset : entity.Offset+entity.Length]
		}
		urls = append(urls, entityUrl)
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

type CreateBookmarkPayload struct {
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Notes       string   `json:"notes"`
	IsArchived  bool     `json:"is_archived"`
	Unread      bool     `json:"unread"`
	Shared      bool     `json:"shared"`
	TagNames    []string `json:"tag_names"`
}

type LinkdingRepository interface {
	CreateBookmark(payload *CreateBookmarkPayload) error
}

type linkdingRepository struct {
	baseUrl  string
	apiToken string
}

func (l *linkdingRepository) CreateBookmark(payload *CreateBookmarkPayload) error {
	postBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	postBodyBuffer := bytes.NewBuffer(postBody)

	path, err := url.JoinPath(l.baseUrl, "api/bookmarks/")
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", path, postBodyBuffer)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ApplicationJson)
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", l.apiToken))
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 201 {
		log.Printf("%s", respBody)
		return fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}
	return nil
}

func NewLinkdingRepository(baseUrl, apiToken string) LinkdingRepository {
	return &linkdingRepository{baseUrl, apiToken}
}

type PageInfo struct {
	url         string
	title       string
	description string
}

type PageInfoService interface {
	GetPageInfo(url string) (*PageInfo, error)
}

type pageInfoService struct {
}

func NewPageInfoService() PageInfoService {
	return &pageInfoService{}
}

func (p *pageInfoService) GetPageInfo(url string) (*PageInfo, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	info := htmlinfo.NewHTMLInfo()
	info.AllowOembedFetching = true

	ct := resp.Header.Get("Content-Type")
	if err = info.Parse(resp.Body, &url, &ct); err != nil {
		return nil, err
	}

	oembed := info.GenerateOembedFor(url)
	output := &PageInfo{
		url: url,
	}
	if oembed != nil {
		output.title = oembed.Title
		output.description = oembed.Description
	} else {
		output.title = info.Title
		output.description = info.Description
	}
	return output, nil
}

type LinkService interface {
	Save(url string) error
}

type linkdingLinkService struct {
	repository      LinkdingRepository
	pageInfoService PageInfoService
}

func NewLinkdingLinkService(repository LinkdingRepository, pageInfoService PageInfoService) LinkService {
	return &linkdingLinkService{repository, pageInfoService}
}

func (l *linkdingLinkService) Save(url string) error {
	normalizedUrl, err := urlx.NormalizeString(url)
	if err != nil {
		return err
	}
	pageInfo, err := l.pageInfoService.GetPageInfo(normalizedUrl)
	if err != nil {
		return err
	}
	payload := CreateBookmarkPayload{
		URL:         normalizedUrl,
		Title:       pageInfo.title,
		Description: pageInfo.description,
		Notes:       "",
		IsArchived:  false,
		Unread:      true,
		Shared:      false,
		TagNames:    []string{},
	}
	return l.repository.CreateBookmark(&payload)
}

type bot struct {
	chatId           int64
	allowedUsernames []string
	urlExtractor     UrlExtractor
	linkService      LinkService
	echotron.API
}

func (b *bot) maybeSendMessage(text string) {
	_, err := b.SendMessage(text, b.chatId, nil)
	if err != nil {
		log.Printf("Send message error: %v", err)
	}
}

func (b *bot) Update(update *echotron.Update) {
	msg := update.Message
	if msg == nil {
		return
	}

	if !contains(b.allowedUsernames, msg.From.Username) {
		b.maybeSendMessage("You are not allowed to use this bot")
		return
	}

	log.Printf("Received message: %v", msg)

	urls := b.urlExtractor(msg)
	if len(urls) == 0 {
		b.maybeSendMessage("No URLs found in the message")
		return
	}

	firstUrl := urls[0]
	err := b.linkService.Save(firstUrl)
	if err != nil {
		log.Printf("Couldn't save a link: %v", err)
		b.maybeSendMessage("Error")
		return
	}
	b.maybeSendMessage("Saved!")
}

type BotFactory interface {
	NewBot() echotron.NewBotFn
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
) BotFactory {
	return &botFactory{
		tgToken:          tgToken,
		allowedUsernames: allowedUsernames,
		urlExtractor:     urlExtractor,
		linkService:      linkService,
		api:              api,
	}
}

func (b *botFactory) NewBot() echotron.NewBotFn {
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
	LinkdingBaseUrl  string   `mapstructure:"LINKDING_BASE_URL"`
	LinkdingApiToken string   `mapstructure:"LINKDING_API_TOKEN"`
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

	linkdingRepository := NewLinkdingRepository(config.LinkdingBaseUrl, config.LinkdingApiToken)
	pageInfoService := NewPageInfoService()
	linkService := NewLinkdingLinkService(linkdingRepository, pageInfoService)
	urlExtractor := GetUrlsWithExtractors(GetUrlsFromEntities, GetUrlsFromLinkPreview)
	botFactory := NewBotFactory(
		config.Token,
		config.AllowedUsernames,
		urlExtractor,
		linkService,
		api,
	)

	dsp := echotron.NewDispatcher(config.Token, botFactory.NewBot())
	log.Println("Dispatcher constructed")

	for {
		log.Println("Polling...")
		log.Println(dsp.Poll())

		time.Sleep(5 * time.Second)
	}
}
