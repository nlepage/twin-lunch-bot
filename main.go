package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"cloud.google.com/go/datastore"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	secretmanagerpb "google.golang.org/genproto/googleapis/cloud/secretmanager/v1"
)

var (
	logger = log.New(os.Stdout, "main: ", log.Lshortfile|log.LstdFlags)
	debug  bool

	userRegexp = regexp.MustCompile(`<@([^\|]+)\|[^>]+>`)

	twinLunches     = make(map[string]string)
	twinLunchAdmins = make(map[string]struct{})

	slackClient     *socketmode.Client
	datastoreClient *datastore.Client
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		logger.Fatal(err)
	}

	debug = os.Getenv("DEBUG") == "true"

	http.HandleFunc("/_ah/warmup", func(w http.ResponseWriter, r *http.Request) {
		start(r.Context())
	})

	var port = os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	for _, twinLunchAdmin := range strings.Split(os.Getenv("TWIN_LUNCH_ADMINS"), ",") {
		if twinLunchAdmin == "" {
			continue
		}
		twinLunchAdmins[twinLunchAdmin] = struct{}{}
	}

	logger.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		logger.Fatal(err)
	}
}

func start(ctx context.Context) {
	logger.Println("received warmup request, starting...")

	var secrets, err = getSecrets(ctx, "SLACK_BOT_TOKEN", "SLACK_APP_TOKEN")
	if err != nil {
		log.Fatal(err)
	}

	slackClient = socketmode.New(
		slack.New(
			secrets["SLACK_BOT_TOKEN"],
			slack.OptionDebug(debug),
			slack.OptionLog(log.New(os.Stdout, "slack: ", log.Lshortfile|log.LstdFlags)),
			slack.OptionAppLevelToken(secrets["SLACK_APP_TOKEN"]),
		),
		socketmode.OptionDebug(debug),
		socketmode.OptionLog(log.New(os.Stdout, "socketmode: ", log.Lshortfile|log.LstdFlags)),
	)

	if datastoreClient, err = datastore.NewClient(context.Background(), ""); err != nil {
		logger.Fatal(err)
	}

	var messages = make(chan *slackevents.MessageEvent)
	var filteredMessages = make(chan *slackevents.MessageEvent)
	var commands = make(chan slack.SlashCommand)

	go receiveEvents(slackClient, messages, commands)
	go filterMessages(messages, filteredMessages)
	go run(filteredMessages, commands)

	go runSlackClient()
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

func run(messages <-chan *slackevents.MessageEvent, commands <-chan slack.SlashCommand) {
	for {
		select {
		case message := <-messages:
			if twinLunch, ok := twinLunches[message.User]; ok {
				if err := forwardTwinLunchMessage(twinLunch, message.Text); err != nil {
					log.Println(err)
				}
			} else {
				if err := sendBotMessageToChannel(message.Channel, "Désolé tu n'as pas de Twin Lunch :crying_cat_face:"); err != nil {
					log.Println(err)
				}
			}

		case command := <-commands:
			if _, ok := twinLunchAdmins[command.UserID]; !ok {
				if err := sendBotMessageToUser(command.UserID, "Désolé mais tu n'as pas les droits pour administrer les Twin Lunch :no_entry_sign:"); err != nil {
					logger.Println(err)
				}
				continue
			}

			switch command.Command {
			case "/twinlunch-add":
				var matches = userRegexp.FindAllStringSubmatch(command.Text, -1)

				if len(matches) != 2 {
					if err := sendBotMessageToUser(command.UserID, "Tu dois donner deux personnes pour créer un Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				var user1, user2 = matches[0][1], matches[1][1]

				if user1 == user2 {
					if err := sendBotMessageToUser(command.UserID, "Tu dois donner deux personnes différentes pour créer un Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				if _, ok := twinLunches[user1]; ok {
					if err := sendBotMessageToUser(command.UserID, fmt.Sprintf("<@%s> a déjà un Twin Lunch", user1)); err != nil {
						logger.Println(err)
					}
					continue
				}

				if _, ok := twinLunches[user2]; ok {
					if err := sendBotMessageToUser(command.UserID, fmt.Sprintf("<@%s> a déjà un Twin Lunch", user2)); err != nil {
						logger.Println(err)
					}
					continue
				}

				twinLunches[user1], twinLunches[user2] = user2, user1

				if err := sendBotMessageToUser(user1, "Salut ! Ton Twin Lunch a été choisi, tu peux discuter avec lui ou elle dans cette conversation sans révéler ton identité :sunglasses:"); err != nil {
					logger.Println(err)
				}

				if err := sendBotMessageToUser(user2, "Salut ! Ton Twin Lunch a été choisi, tu peux discuter avec lui ou elle dans cette conversation sans révéler ton identité :sunglasses:"); err != nil {
					logger.Println(err)
				}

				if err := sendBotMessageToUser(command.UserID, fmt.Sprintf("J'ai mis en relation <@%s> et <@%s> pour leur Twin Lunch", user1, user2)); err != nil {
					logger.Println(err)
				}

			case "/twinlunch-remove":

				var matches = userRegexp.FindAllStringSubmatch(command.Text, -1)

				if len(matches) != 2 {
					if err := sendBotMessageToUser(command.UserID, "Tu dois donner deux personnes pour supprimer un Twin Lunch"); err != nil {
						logger.Println(err)
					}
					continue
				}

				var user1, user2 = matches[0][1], matches[1][1]

				if twinLunches[user1] != user2 {
					if err := sendBotMessageToUser(command.UserID, fmt.Sprintf("<@%s> et <@%s> ne sont pas en Twin Lunch ensemble", user1, user2)); err != nil {
						logger.Println(err)
					}
					continue
				}

				delete(twinLunches, user1)
				delete(twinLunches, user2)

				if err := sendBotMessageToUser(command.UserID, fmt.Sprintf("J'ai supprimé le Twin Lunch entre <@%s> et <@%s>", user1, user2)); err != nil {
					logger.Println(err)
				}

			case "/twinlunch-list":
				if len(twinLunches) == 0 {
					if err := sendBotMessageToUser(command.UserID, "Il n'y a aucun Twin Lunch"); err != nil {
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

				if err := sendBotMessageToUser(command.UserID, "Voilà la liste des Twin Lunch :\n"+strings.Join(list, "\n")); err != nil {
					logger.Println(err)
				}

			case "/twinlunch-clear":
				twinLunches = make(map[string]string)

				if err := sendBotMessageToUser(command.UserID, "J'ai supprimé tous les Twin Lunch :fire:"); err != nil {
					logger.Println(err)
				}
			}
		}
	}
}

func forwardTwinLunchMessage(user string, text string) error {
	var channel, err = getChannelForUser(user)
	if err != nil {
		return err
	}

	if _, _, err := slackClient.PostMessage(
		channel,
		slack.MsgOptionText(text, false),
		slack.MsgOptionIconEmoji("question"),
		slack.MsgOptionUsername("Ton Twin Lunch"),
	); err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}

	return nil
}

func sendBotMessageToUser(user string, text string) error {
	var channel, err = getChannelForUser(user)
	if err != nil {
		return err
	}

	if _, _, err := slackClient.PostMessage(
		channel,
		slack.MsgOptionIconEmoji("robot_face"),
		slack.MsgOptionUsername("Twin Lunch Bot"),
		slack.MsgOptionText("_bip bip_ "+text, false),
	); err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}
	return nil
}

func sendBotMessageToChannel(channel string, text string) error {
	if _, _, err := slackClient.PostMessage(
		channel,
		slack.MsgOptionIconEmoji("robot_face"),
		slack.MsgOptionUsername("Twin Lunch Bot"),
		slack.MsgOptionText("_bip bip_ "+text, false),
	); err != nil {
		return fmt.Errorf("error sending message: %w", err)
	}
	return nil
}

func getChannelForUser(user string) (string, error) {
	var channel, _, _, err = slackClient.OpenConversation(&slack.OpenConversationParameters{Users: []string{user}})
	if err != nil {
		return "", fmt.Errorf("error opening conversation: %w", err)
	}
	return channel.ID, nil
}

func runSlackClient() {
	logger.Println("running slack client...")

	if err := slackClient.Run(); err != nil {
		logger.Fatal(err)
	}
}

func getSecrets(ctx context.Context, names ...string) (map[string]string, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("error connecting to secret manager: %w", err)
	}
	defer client.Close()

	var secrets = make(map[string]string)

	for _, name := range names {
		result, err := client.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
			Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", os.Getenv("GOOGLE_CLOUD_PROJECT"), name),
		})
		if err != nil {
			return nil, fmt.Errorf("error reading secret: %w", err)
		}

		secrets[name] = string(result.Payload.Data)
	}

	return secrets, nil
}
