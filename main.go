package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

var logger = log.New(os.Stdout, "main: ", log.Lshortfile|log.LstdFlags)

func main() {
	if err := godotenv.Load(); err != nil {
		logger.Fatal(err)
	}

	var debug = os.Getenv("DEBUG") == "true"
	var slackAppToken, slackBotToken = os.Getenv("SLACK_APP_TOKEN"), os.Getenv("SLACK_BOT_TOKEN")

	var api = slack.New(
		slackBotToken,
		slack.OptionDebug(debug),
		slack.OptionLog(log.New(os.Stdout, "slack: ", log.Lshortfile|log.LstdFlags)),
		slack.OptionAppLevelToken(slackAppToken),
	)

	var client = socketmode.New(
		api,
		socketmode.OptionDebug(debug),
		socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.Lshortfile|log.LstdFlags)),
	)

	var messageEvts = make(chan *slackevents.MessageEvent)
	var incomingMessageEvts = make(chan *slackevents.MessageEvent)

	go receiveMessageEvents(client, messageEvts)
	go filterBotMessages(messageEvts, incomingMessageEvts)
	go handleMessageEvents(api, incomingMessageEvts)

	logger.Println("Running slack client...")

	if err := client.Run(); err != nil {
		logger.Fatal(err)
	}
}

func receiveMessageEvents(client *socketmode.Client, out chan<- *slackevents.MessageEvent) {
	for clientEvt := range client.Events {
		if clientEvt.Type != socketmode.EventTypeEventsAPI {
			continue
		}

		outerEvt := clientEvt.Data.(slackevents.EventsAPIEvent)

		if outerEvt.Type != slackevents.CallbackEvent {
			logger.Println("ignoring slack outer event", outerEvt)
			continue
		}

		innerEvt := outerEvt.InnerEvent
		if innerEvt.Type != slackevents.Message {
			logger.Println("ignoring slack inner event", innerEvt)
			continue
		}

		out <- innerEvt.Data.(*slackevents.MessageEvent)

		client.Ack(*clientEvt.Request)
	}
}

func filterBotMessages(in <-chan *slackevents.MessageEvent, out chan<- *slackevents.MessageEvent) {
	for messageEvt := range in {
		if messageEvt.BotID != "" {
			continue
		}
		out <- messageEvt
	}
}

func handleMessageEvents(api *slack.Client, in <-chan *slackevents.MessageEvent) {
	for messageEvt := range in {
		logger.Printf("%s: %s\n", messageEvt.User, messageEvt.Text)

		if messageEvt.User == "U15ATTX71" && messageEvt.Text == "test" {
			if _, _, err := api.PostMessage(
				messageEvt.Channel,
				slack.MsgOptionText(":ah2:", false),
			); err != nil {
				logger.Println("error sending message:", err)
			}
		}
	}
}
