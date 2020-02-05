package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/rudi9719/loggy"
	"samhofi.us/x/keybase"
)

var (
	// Keybase setup
	k = keybase.NewKeybase()

	// Logging Setup
	logOpts = loggy.LogOpts{
		UseStdout: true,
		Level:     5,
		//      KBTeam: "nightmarehaus.logs",
		//	KBChann: "general",
		//	ProgName: "rss",
	}
	log = loggy.NewLogger(logOpts)

	// gofeed setup
	fp = gofeed.NewParser()
)

func SetupCleanup() {
	log.LogInfo("Setting up cleanup func")
	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		_, err := k.ClearCommands()
		if err != nil {
			log.LogCritical("Error on k.ClearCommands during cleanup")
			log.LogDebug(fmt.Sprintf("```%+v```", err))
		}
		log.LogCritical("Shutting down rss bot")
		os.Exit(0)
	}()
}

func main() {
	log.LogInfo("Starting rss bot")
	if !k.LoggedIn {
		log.LogPanic("Keybase is not logged in.")
	}
	log.LogInfo("Clearing commands to start bot")
	_, err := k.ClearCommands()
	if err != nil {
		log.LogWarn("err was not nil on k.ClearCommands()")
		log.LogDebug(fmt.Sprintf("k.ClearCommands() error: ```%+v```", err))
	}
	go SetupCleanup()
	c := keybase.BotAdvertisement{
		Type: "public",
		BotCommands: []keybase.BotCommand{
			keybase.NewBotCommand("rss subscribe <url>", "Subscribe to an RSS Feed by URL", ""),
			keybase.NewBotCommand("rss get <id>", "Get message by ID", ""),
			keybase.NewBotCommand("rss status", "Get RSS Feed Status", ""),
			keybase.NewBotCommand("rss refresh", "Refresh RSS Feed", ""),
			keybase.NewBotCommand("rss unsubscribe", "Unsubscribe from RSS Feed by URL", ""),
		},
	}
	k.AdvertiseCommand(c)

	k.Run(func(api keybase.ChatAPI) {
		handleMessage(api)
	})
	k.ClearCommands()
	log.LogPanic("Keybase.Run() has returned.")
}

func subscribe(api keybase.ChatAPI) {
	log.LogInfo("Creating KV for subscribe")
	slices := strings.Split(api.Msg.Content.Text.Body, " ")
	if len(slices) < 3 {
		log.LogWarn("Not enough parameters to subscribe")
		log.LogDebug(fmt.Sprintf("%s: %s", api.Msg.Sender.Username, api.Msg.Content.Text.Body))
		return
	}
	if !strings.Contains(slices[2], "http") {
		log.LogWarn("URL Not detected in subscribe")
		log.LogDebug(fmt.Sprintf("%s: %s", api.Msg.Sender.Username, slices[2]))
	}
	url := slices[2]
	subKey := k.NewKV(api.Msg.Channel.Name)
	sub := Subscription{
		Channel: api.Msg.Channel.Name,
		Team:    api.Msg.Channel.MembersType == keybase.TEAM,
		Url:     url,
		User:    api.Msg.Sender.Username,
	}
	jsonData, err := json.Marshal(sub)
	if err != nil {
		log.LogError("Error marshalling json in subscribe()")
		log.LogDebug(fmt.Sprintf("```%+v```", err))
		return
	}
	_, err = subKey.Put("keybase-rss", "config", string(jsonData))
	if err != nil {
		log.LogError("Error creating config key")
		log.LogErrorType(err)
	}
}

func refresh(api keybase.ChatAPI) {
	log.LogInfo("Refreshing RSS Feed")
	chat := k.NewChat(api.Msg.Channel)
	subKey := k.NewKV(api.Msg.Channel.Name)
	resKey, err := subKey.Get("keybase-rss", "config")
	if err != nil {
		log.LogError("Error getting result key in refresh()")
		log.LogDebug(fmt.Sprintf("```%+v```", err))
		return
	}
	existingKeys, err := subKey.Keys("keybase-rss")
	if err != nil {
		log.LogError("Error getting existingKeys from subKey")
		log.LogErrorType(err)
		return
	}
	var sub Subscription
	err = json.Unmarshal([]byte(resKey.Result.EntryValue), &sub)
	if err != nil {
		log.LogError("Error unmarshalling json from refresh()")
		log.LogDebug(fmt.Sprintf("```%+v```", err))
		return
	}
	url := sub.Url
	feed, err := fp.ParseURL(url)
	if err != nil {
		log.LogError(fmt.Sprintf("Error subscribing to URL %s", url))
		log.LogDebug(fmt.Sprintf("subscribe(url) error: ```%+v```", err))
		return
	}
	log.LogDebug(fmt.Sprintf("Feed: ```%+v```", feed))
	for _, item := range feed.Items {
		exists := false
		var post Post
		post.Title = strings.Replace(item.Title, "<br />", " ", -1)
		post.Description = strings.Replace(item.Description, "<br />", " ", -1)
		post.Link = item.Link
		post.Id = item.GUID
		post.Pubdate = item.Published
		if post.Id == "" {
			testSlice := strings.Split(item.Link, "id=")
			testID := testSlice[len(testSlice)-1]
			post.Id = testID
		}
		for _, key := range existingKeys.Result.EntryKeys {
			if key.EntryKey == post.Id {
				exists = true
				break
			}
		}
		if time.Since(*item.PublishedParsed) < (10*24)*time.Hour && exists {
			_, delErr := subKey.Delete("keybase-rss", string(post.Id))
			if delErr != nil {
				log.LogError("Error deleting expired key from keybase-rss namespace")
				log.LogDebug(fmt.Sprintf("Key: %s\n ```%+v```", string(post.Id), api.Msg.Channel))
			}
		}
		if time.Since(*item.PublishedParsed) < 24*time.Hour && !exists {
			chat.Send(fmt.Sprintf(">%s\n```%s\n```\n>%s\n%s",
				post.Title, post.Description, post.Pubdate, post.Link))
		} else {
			exists = true // Don't store tags unless they were generated today - RATE LIMIT
		}
		if exists {
			continue
		}
		jsonPost, jsonErr := json.Marshal(post)
		if jsonErr != nil {
			log.LogError("Error marshalling json in refresh()")
			log.LogErrorType(jsonErr)
		}
		_, err = subKey.Put("keybase-rss", string(post.Id), string(jsonPost))
		if err != nil {
			log.LogError(fmt.Sprintf("Error on KV.Put() for jsonPost ID  %s", post.Id))
			log.LogDebug(fmt.Sprintf("```%+v```", string(jsonPost)))

		}
	}

}

// TODO: Periodically go through and refresh each team <------------------------------------------------

func getById(api keybase.ChatAPI) {
	chat := k.NewChat(api.Msg.Channel)
	slices := strings.Split(api.Msg.Content.Text.Body, " ")
	if len(slices) < 3 {
		log.LogWarn("Not enough parameters for getById")
		log.LogDebug(fmt.Sprintf("%s: %s", api.Msg.Sender.Username, api.Msg.Content.Text.Body))
		return
	}
	subKey := k.NewKV(api.Msg.Channel.Name)
	resKey, err := subKey.Get("keybase-rss", slices[2])
	if err != nil {
		log.LogError("Error getting result key in refresh()")
		log.LogDebug(fmt.Sprintf("```%+v```", err))
		return
	}
	chat.Send(fmt.Sprintf("```%+v```", resKey.Result.EntryValue))
}

func status(api keybase.ChatAPI) {
	chat := k.NewChat(api.Msg.Channel)
	subKey := k.NewKV(api.Msg.Channel.Name)
	resKey, err := subKey.Get("keybase-rss", "config")
	if err != nil {
		log.LogError("Error getting result key in status()")
		log.LogDebug(fmt.Sprintf("```%+v```", err))
		return
	}
	chat.Send(fmt.Sprintf("Current status: ```%+v```", resKey.Result.EntryValue))
}

func unsubscribe(api keybase.ChatAPI) {
	subKey := k.NewKV(api.Msg.Channel.Name)
	kvKeys, err := subKey.Keys("keybase-rss")
	if err != nil {
		log.LogError("Error getting subkey's Keys")
		log.LogErrorType(err)
		return
	}
	for _, key := range kvKeys.Result.EntryKeys {
		_, err = subKey.Delete("keybase-rss", key.EntryKey)
		if err != nil {
			log.LogError(fmt.Sprintf("Error deleting subkey %s", key.EntryKey))
			return
		}
	}

}

func handleMessage(api keybase.ChatAPI) {
	if api.Msg.Content.Type != "text" {
		return
	}
	slices := strings.Split(api.Msg.Content.Text.Body, " ")
	if slices[0] != "!rss" || len(slices) < 2 {
		return
	}
	log.LogInfo("rss command detected")
	log.LogDebug(fmt.Sprintf("```%+v```", api))
	switch slices[1] {
	case "get":
		go getById(api)
	case "status":
		go status(api)
	case "refresh":
		go refresh(api)
	case "subscribe":
		go subscribe(api)
	case "unsubscribe":
		go unsubscribe(api)
	default:
		log.LogWarn(fmt.Sprintf("Unrecognized command %s by %s", slices[1], api.Msg.Sender.Username))
	}
}
