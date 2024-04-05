package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"time"
	"unicode/utf16"

	"github.com/dyatlov/go-htmlinfo/htmlinfo"
	"github.com/goware/urlx"
	"github.com/joomcode/errorx"
	log "github.com/sirupsen/logrus"

	"github.com/NicoNex/echotron/v3"
	"github.com/spf13/viper"
)

const (
	ApplicationJson = "application/json"
)

type UrlExtractor func(msg *echotron.Message) []string

func GetUrlsFromEntities(msg *echotron.Message) []string {
	urls := make([]string, 0)
	allEntities := make([]*echotron.MessageEntity, 0, len(msg.Entities)+len(msg.CaptionEntities))
	allEntities = append(allEntities, msg.Entities...)
	allEntities = append(allEntities, msg.CaptionEntities...)
	for _, entity := range allEntities {
		if entity.Type != "url" && entity.Type != "text_link" {
			continue
		}
		entityUrl := entity.URL
		if entityUrl == "" {
			// offset and length are in UTF-16 code units
			entityUrl = sliceUtf16(msg.Text, entity.Offset, entity.Offset+entity.Length)
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

// GetUrlsWithExtractors returns a new UrlExtractor that combines the results of the provided extractors (order preserved)
func GetUrlsWithExtractors(extractors ...UrlExtractor) UrlExtractor {
	return func(msg *echotron.Message) []string {
		urls := make([]string, 0)
		for _, extractor := range extractors {
			urls = append(urls, extractor(msg)...)
		}
		return distinct(urls)
	}
}

func sliceUtf16(s string, start, end int) string {
	return string(utf16.Decode(utf16.Encode([]rune(s))[start:end]))
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
		return errorx.Decorate(err, "failed to marshal payload")
	}
	postBodyBuffer := bytes.NewBuffer(postBody)

	path, err := url.JoinPath(l.baseUrl, "api/bookmarks/")
	if err != nil {
		return errorx.Decorate(err, "failed to join path")
	}

	req, err := http.NewRequest("POST", path, postBodyBuffer)
	if err != nil {
		return errorx.Decorate(err, "failed to create request")
	}

	req.Header.Set("Content-Type", ApplicationJson)
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", l.apiToken))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return errorx.Decorate(err, "failed to send request")
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return errorx.Decorate(err, "failed to read response body")
	}

	if resp.StatusCode != http.StatusCreated {
		log.Printf("%s", respBody)
		return errorx.IllegalState.New("unexpected status code %d", resp.StatusCode)
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
		return nil, errorx.Decorate(err, "failed to fetch URL")
	}
	defer resp.Body.Close()

	info := htmlinfo.NewHTMLInfo()
	info.AllowOembedFetching = true

	ct := resp.Header.Get("Content-Type")
	if err = info.Parse(resp.Body, &url, &ct); err != nil {
		return nil, errorx.Decorate(err, "failed to parse page info")
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
	log.Debugf("Saving url: %s", url)

	normalizedUrl, err := urlx.NormalizeString(url)
	if err != nil {
		return errorx.Decorate(err, "failed to normalize URL")
	}
	log.Debugf("Normalized URL: %s", normalizedUrl)

	fromTime := time.Now()
	pageInfo, err := l.pageInfoService.GetPageInfo(normalizedUrl)
	if err != nil {
		return errorx.Decorate(err, "failed to get page info")
	}
	toTime := time.Now()
	log.Debugf("Completed page info fetch in %s", toTime.Sub(fromTime))

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

	fromTime = time.Now()
	err = l.repository.CreateBookmark(&payload)
	toTime = time.Now()
	log.WithField("error", err).Debugf("Completed bookmark creation in %s", toTime.Sub(fromTime))

	return err
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
		log.Debugf("Username %s is not allowed", msg.From.Username)
		b.maybeSendMessage("You are not allowed to use this bot")
		return
	}

	log.Debugf("Received message: %v", msg)

	urls := b.urlExtractor(msg)
	if len(urls) == 0 {
		log.Debug("No URLs found")
		b.maybeSendMessage("No URLs found in the message")
		return
	}

	firstUrl := urls[0]
	err := b.linkService.Save(firstUrl)
	if err != nil {
		log.Debugf("Couldn't save a link: %+v", err)
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
	DebugLogging     bool     `mapstructure:"DEBUG_LOGGING"`
}

func parseConfig(i interface{}) error {
	r := reflect.TypeOf(i)
	for r.Kind() == reflect.Ptr {
		r = r.Elem()
	}
	for i := 0; i < r.NumField(); i++ {
		env := r.Field(i).Tag.Get("mapstructure")
		if err := viper.BindEnv(env); err != nil {
			return errorx.Decorate(err, "failed to bind env variable")
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
		log.Fatalf("%+v", errorx.Decorate(err, "failed to read config"))
	}
	config := &envConfig{}
	if err := parseConfig(config); err != nil {
		log.Fatalf("%+v", errorx.Decorate(err, "failed to parse config"))
	}
	return config
}

func validateConfig(config *envConfig) error {
	if config.Token == "" {
		return errorx.IllegalArgument.New("env TOKEN is required")
	}
	if len(config.AllowedUsernames) == 0 {
		return errorx.IllegalArgument.New("at least one allowed username is required (env ALLOWED_USERNAMES)")
	}
	if config.LinkdingApiToken == "" {
		return errorx.IllegalArgument.New("env LINKDING_API_TOKEN is required")
	}
	if config.LinkdingBaseUrl == "" {
		return errorx.IllegalArgument.New("env LINKDING_BASE_URL is required")
	}
	return nil
}

func main() {
	log.SetOutput(os.Stdout)
	config := loadEnvVariables()
	if config.DebugLogging {
		log.SetLevel(log.DebugLevel)
	}
	err := validateConfig(config)
	if err != nil {
		log.Fatalf("%+v", errorx.Decorate(err, "config validation failed"))
	}
	log.Println("Config loaded successfully")
	log.Printf("Allowed usernames: %v", config.AllowedUsernames)

	api := echotron.NewAPI(config.Token)

	res, err := api.GetMe()
	if err != nil {
		log.Fatalf("%+v", errorx.Decorate(err, "failed to get bot info"))
	}
	log.Printf("Bot username: @%s", res.Result.Username)

	linkdingRepository := NewLinkdingRepository(config.LinkdingBaseUrl, config.LinkdingApiToken)
	pageInfoService := NewPageInfoService()
	linkService := NewLinkdingLinkService(linkdingRepository, pageInfoService)
	urlExtractor := GetUrlsWithExtractors(GetUrlsFromLinkPreview, GetUrlsFromEntities)
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
