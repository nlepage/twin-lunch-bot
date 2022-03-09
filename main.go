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

	go receiveMessageEvents(client, messageEvts)
	go printMessageEvents(messageEvts)

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

func printMessageEvents(in <-chan *slackevents.MessageEvent) {
	for messageEvt := range in {
		logger.Printf("%s: %s\n", messageEvt.User, messageEvt.Text)
	}
}
