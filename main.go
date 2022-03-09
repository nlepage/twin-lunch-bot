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

var twinLunches = make(map[string]string)

func main() {
	if err := godotenv.Load(); err != nil {
		logger.Fatal(err)
	}

	var debug = os.Getenv("DEBUG") == "true"
	var slackAppToken, slackBotToken = os.Getenv("SLACK_APP_TOKEN"), os.Getenv("SLACK_BOT_TOKEN")

	var client = socketmode.New(
		slack.New(
			slackBotToken,
			slack.OptionDebug(debug),
			slack.OptionLog(log.New(os.Stdout, "slack: ", log.Lshortfile|log.LstdFlags)),
			slack.OptionAppLevelToken(slackAppToken),
		),
		socketmode.OptionDebug(debug),
		socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.Lshortfile|log.LstdFlags)),
	)

	var messageEvts = make(chan *slackevents.MessageEvent)
	var incomingMessageEvts = make(chan *slackevents.MessageEvent)

	go receiveMessages(client, messageEvts)
	go filterMessages(messageEvts, incomingMessageEvts)
	go run(client, incomingMessageEvts)

	logger.Println("Running slack client...")

	if err := client.Run(); err != nil {
		logger.Fatal(err)
	}
}

func receiveMessages(client *socketmode.Client, out chan<- *slackevents.MessageEvent) {
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

func filterMessages(in <-chan *slackevents.MessageEvent, out chan<- *slackevents.MessageEvent) {
	for messageEvt := range in {
		if messageEvt.BotID != "" {
			continue
		}
		if messageEvt.ChannelType != slack.TYPE_IM {
			continue
		}
		out <- messageEvt
	}
}

func run(client *socketmode.Client, messages <-chan *slackevents.MessageEvent) {
	for {
		select {
		case message := <-messages:
			if twinLunch, ok := twinLunches[message.User]; ok {
				forwardMessage(client, twinLunch, message.Text)
			} else {
				if _, _, err := client.PostMessage(
					message.Channel,
					slack.MsgOptionText(":robot_face: _bip bip_ Désolé vous n'avez pas de Twin Lunch :crying_cat_face:", false),
				); err != nil {
					logger.Println("error sending message:", err)
				}
			}
		}
	}
}

func forwardMessage(client *socketmode.Client, user string, text string) {
	var channel, _, _, err = client.OpenConversation(&slack.OpenConversationParameters{Users: []string{user}})
	if err != nil {
		logger.Println("error opening conversation:", err)
	}

	if _, _, err := client.PostMessage(channel.ID, slack.MsgOptionText(text, false)); err != nil {
		logger.Println("error sending message:", err)
	}
}
