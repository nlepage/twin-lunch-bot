package main

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"

	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

var logger = log.New(os.Stdout, "main: ", log.Lshortfile|log.LstdFlags)

var twinLunches = make(map[string]string)

var userRegexp = regexp.MustCompile("<@([^\\|]+)\\|[^>]+>")

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

	var messages = make(chan *slackevents.MessageEvent)
	var filteredMessages = make(chan *slackevents.MessageEvent)
	var commands = make(chan slack.SlashCommand)

	go receiveEvents(client, messages, commands)
	go filterMessages(messages, filteredMessages)
	go run(client, filteredMessages, commands)

	logger.Println("Running slack client...")

	if err := client.Run(); err != nil {
		logger.Fatal(err)
	}
}

func receiveEvents(client *socketmode.Client, messages chan<- *slackevents.MessageEvent, commands chan<- slack.SlashCommand) {
	for clientEvt := range client.Events {
		switch clientEvt.Type {

		case socketmode.EventTypeEventsAPI:
			var outerEvt = clientEvt.Data.(slackevents.EventsAPIEvent)

			if outerEvt.Type != slackevents.CallbackEvent {
				logger.Println("ignoring slack outer event", outerEvt)
				continue
			}

			var innerEvt = outerEvt.InnerEvent
			if innerEvt.Type != slackevents.Message {
				logger.Println("ignoring slack inner event", innerEvt)
				continue
			}

			messages <- innerEvt.Data.(*slackevents.MessageEvent)

			client.Ack(*clientEvt.Request)

		case socketmode.EventTypeSlashCommand:
			commands <- clientEvt.Data.(slack.SlashCommand)

			client.Ack(*clientEvt.Request)
		}
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

func run(client *socketmode.Client, messages <-chan *slackevents.MessageEvent, commands <-chan slack.SlashCommand) {
	for {
		select {
		case message := <-messages:
			if twinLunch, ok := twinLunches[message.User]; ok {
				if err := forwardTwinLunchMessage(client, twinLunch, message.Text); err != nil {
					log.Println(err)
				}
			} else {
				if err := sendBotMessageToChannel(client, message.Channel, "Désolé tu n'as pas de Twin Lunch :crying_cat_face:"); err != nil {
					log.Println(err)
				}
			}

		case command := <-commands:
			switch command.Command {
			case "/twinlunch-add":
				var matches = userRegexp.FindAllStringSubmatch(command.Text, -1)

				if len(matches) != 2 {
					if err := sendBotMessageToUser(client, command.UserID, "Tu dois donner deux personnes pour créer un Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				var user1, user2 = matches[0][1], matches[1][1]

				if user1 == user2 {
					if err := sendBotMessageToUser(client, command.UserID, "Tu dois donner deux personnes différentes pour créer un Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				if _, ok := twinLunches[user1]; ok {
					if err := sendBotMessageToUser(client, command.UserID, fmt.Sprintf("<@%s> a déjà un Twin Lunch", user1)); err != nil {
						logger.Println(err)
					}
					continue
				}

				if _, ok := twinLunches[user2]; ok {
					if err := sendBotMessageToUser(client, command.UserID, fmt.Sprintf("<@%s> a déjà un Twin Lunch", user2)); err != nil {
						logger.Println(err)
					}
					continue
				}

				twinLunches[user1], twinLunches[user2] = user2, user1

				if err := sendBotMessageToUser(client, user1, "Salut ! Ton Twin Lunch a été choisi, tu peux discuter avec lui ou elle dans cette conversation sans révéler ton identité :sunglasses:"); err != nil {
					logger.Println(err)
				}

				if err := sendBotMessageToUser(client, user2, "Salut ! Ton Twin Lunch a été choisi, tu peux discuter avec lui ou elle dans cette conversation sans révéler ton identité :sunglasses:"); err != nil {
					logger.Println(err)
				}

				if err := sendBotMessageToUser(client, command.UserID, fmt.Sprintf("J'ai mis en relation <@%s> et <@%s> pour leur Twin Lunch", user1, user2)); err != nil {
					logger.Println(err)
				}

			case "/twinlunch-remove":

				var matches = userRegexp.FindAllStringSubmatch(command.Text, -1)

				if len(matches) != 2 {
					if err := sendBotMessageToUser(client, command.UserID, "Tu dois donner deux personnes pour supprimer un Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				var user1, user2 = matches[0][1], matches[1][1]

				if twinLunches[user1] != user2 {
					if err := sendBotMessageToUser(client, command.UserID, fmt.Sprintf("<@%s> et <@%s> ne sont pas en Twin Lunch ensemble", user1, user2)); err != nil {
						logger.Println(err)
					}
					continue
				}

				delete(twinLunches, user1)
				delete(twinLunches, user2)

				if err := sendBotMessageToUser(client, command.UserID, fmt.Sprintf("J'ai supprimé le Twin Lunch entre <@%s> et <@%s>", user1, user2)); err != nil {
					logger.Println(err)
				}

			case "/twinlunch-list":
				if len(twinLunches) == 0 {
					if err := sendBotMessageToUser(client, command.UserID, "Il n'y a aucun Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				var list = make([]string, 0, len(twinLunches)/2)
				var listed = make(map[string]struct{}, len(twinLunches))
				for user1, user2 := range twinLunches {
					if _, ok := listed[user1]; ok {
						continue
					}
					list = append(list, fmt.Sprintf(" • <@%s> et <@%s>", user1, user2))
					listed[user1], listed[user2] = struct{}{}, struct{}{}
				}

				if err := sendBotMessageToUser(client, command.UserID, "Voilà la liste des Twin Lunch :\n"+strings.Join(list, "\n")); err != nil {
					logger.Println(err)
				}

			case "/twinlunch-clear":
				twinLunches = make(map[string]string)

				if err := sendBotMessageToUser(client, command.UserID, "J'ai supprimé tous les Twin Lunch :fire:"); err != nil {
					logger.Println(err)
				}
			}
		}
	}
}

func forwardTwinLunchMessage(client *socketmode.Client, user string, text string) error {
	var channel, err = getChannelForUser(client, user)
	if err != nil {
		return err
	}

	if _, _, err := client.PostMessage(
		channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionIconEmoji("question"),
		slack.MsgOptionUsername("Ton Twin Lunch"),
	); err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}

	return nil
}

func sendBotMessageToUser(client *socketmode.Client, user string, text string) error {
	var channel, err = getChannelForUser(client, user)
	if err != nil {
		return err
	}

	if _, _, err := client.PostMessage(
		channel,
		slack.MsgOptionIconEmoji("robot_face"),
		slack.MsgOptionUsername("Twin Lunch Bot"),
		slack.MsgOptionText("_bip bip_ "+text, false),
	); err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}
	return nil
}

func sendBotMessageToChannel(client *socketmode.Client, channel string, text string) error {
	if _, _, err := client.PostMessage(
		channel,
		slack.MsgOptionIconEmoji("robot_face"),
		slack.MsgOptionUsername("Twin Lunch Bot"),
		slack.MsgOptionText("_bip bip_ "+text, false),
	); err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}
	return nil
}

func getChannelForUser(client *socketmode.Client, user string) (string, error) {
	var channel, _, _, err = client.OpenConversation(&slack.OpenConversationParameters{Users: []string{user}})
	if err != nil {
		return "", fmt.Errorf("error opening conversation: %w", err)
	}
	return channel.ID, nil
}
