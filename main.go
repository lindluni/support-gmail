package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"

	"github.com/google/go-github/v35/github"
	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

// Retrieve a token, saves the token, then returns the generated client.
func getClient(config *oauth2.Config) *http.Client {
	log.Println("Attempting to retrieve G-Mail token")
	tok, err := getToken()
	if err != nil {
		log.Panicf("Unable to retrieve token: %v", err)
	}
	log.Println("G-Mail token retrieved successfully")

	log.Println("Creating G-Mail client config")
	return config.Client(context.Background(), tok)
}

// Retrieves a token from a local file.
func getToken() (*oauth2.Token, error) {
	log.Println("Retrieving G-Mail token from environment")
	token := os.Getenv("INPUT_TOKEN")
	tok := &oauth2.Token{}
	log.Println("Marshalling G-Mail token")
	err := json.Unmarshal([]byte(token), tok)
	return tok, err
}

type Email struct {
	FromName  string
	FromEmail string
	ToName    string
	ToEmail   string
	Subject   string
	Message   string
}

type Event struct {
	Issue struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	} `json:"issue"`

	Organization struct {
		Login string `json:"login"`
	} `json:"organization"`

	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
}

type GitHubClient struct {
	client *github.Client
	event  *Event
}

func main() {
	log.Println("Reading GitHub event payload file")
	bytes, err := os.ReadFile(os.Getenv("GITHUB_EVENT_PATH"))
	if err != nil {
		log.Panicf("Unable to read file: %v", err)
	}

	log.Println("Attempting to unmarshal GitHub event payload")
	var event *Event
	err = json.Unmarshal(bytes, &event)
	if err != nil {
		log.Panicf("Unable to unmarshal even payload: %v", err)
	}

	log.Println("Initializing GitHub client")
	client := &GitHubClient{event: event}
	client.initGitHubClient()

	log.Println("Retrieving G-Mail credentials")
	config, err := google.ConfigFromJSON([]byte(os.Getenv("INPUT_CREDENTIALS")), gmail.GmailSendScope) // If modifying these scopes, delete your previously saved token.json.
	if err != nil {
		client.notifyFailure(fmt.Errorf("unable to parse client secret file to config: %v", err))
	}
	log.Println("G-Mail credentials retrieved")

	log.Println("Fetching G-Mail client config")
	gmailClient := getClient(config)

	log.Println("Creating G-Mail client")
	srv, err := gmail.New(gmailClient)
	if err != nil {
		client.notifyFailure(fmt.Errorf("unable to retrieve Gmail client: %v", err))
	}
	log.Println("G-Mail client created successfully")

	command := os.Getenv("INPUT_COMMAND")
	log.Printf("Attempting to parse command: [%s]\n", command)
	approverEmail, userName, userEmail, err := parseCommand(command)
	if err != nil {
		client.notifyFailure(fmt.Errorf("unable to parse command [%s]: %v", command, err))
	}
	log.Println("Successfully parsed command")

	log.Println("Forming email object")
	inputFrom := os.Getenv("INPUT_FROM")
	inputTemplate := os.Getenv("INPUT_TEMPLATE")
	em := &Email{
		FromName:  "GitHub",
		FromEmail: inputFrom,
		ToName:    "PM/COR",
		ToEmail:   approverEmail,
		Subject:   "User Access Request",
		Message:   fmt.Sprintf(inputTemplate, userName, userEmail, client.event.Issue.URL),
	}
	from := mail.Address{Name: em.FromName, Address: em.FromEmail}
	to := mail.Address{Name: em.ToName, Address: em.ToEmail}

	log.Println("Setting headers")
	header := make(map[string]string)
	header["From"] = from.String()
	header["To"] = to.String()
	if !strings.Contains(os.Getenv("INPUT_COMMAND"), "skip") {
		header["cc"] = inputFrom
	} else {
		log.Printf("Skipping emailing CC list: [%s]\n", inputFrom)
	}
	header["Subject"] = em.Subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = `text/plain; charset="utf-8"`

	log.Println("Appending headers")
	var msg string
	for k, v := range header {
		msg += fmt.Sprintf("%s: %s\r\n", k, v)
	}

	log.Println("Appending message")
	msg += "\r\n" + em.Message

	log.Println("Encoding email")
	gmsg := gmail.Message{
		Raw: base64.RawURLEncoding.EncodeToString([]byte(msg)),
	}

	log.Println("Attempting to send email")
	_, err = srv.Users.Messages.Send("me", &gmsg).Do()
	if err != nil {
		client.notifyFailure(fmt.Errorf("Unable to send email: %v\n", err))
	}
	client.notifySuccess()
	client.addEmailSentLabel()
}

func (c *GitHubClient) initGitHubClient() {
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("INPUT_GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)
	c.client = github.NewClient(tc)
}

func (c *GitHubClient) notifyFailure(err error) {
	_, _, clientErr := c.client.Issues.CreateComment(context.Background(), c.event.Organization.Login, c.event.Repository.Name, c.event.Issue.Number, &github.IssueComment{
		Body: github.String(fmt.Sprintf("Failed to send email: %v", err)),
	})
	if clientErr != nil {
		log.Panicf("Unable to create issue failure notice: %v", clientErr)
	}
	log.Panic(err)
}

func (c *GitHubClient) notifySuccess() {
	log.Println("Successfully sent approval email")
	_, _, err := c.client.Issues.CreateComment(context.Background(), c.event.Organization.Login, c.event.Repository.Name, c.event.Issue.Number, &github.IssueComment{
		Body: github.String("Successfully sent approval email"),
	})
	if err != nil {
		log.Panicf("Unable to create issue failure notice: %v", err)
	}
}

func (c *GitHubClient) addEmailSentLabel() {
	log.Println("Adding the email-sent label to the issue")
	_, _, err := c.client.Issues.AddLabelsToIssue(context.Background(), c.event.Organization.Login, c.event.Repository.Name, c.event.Issue.Number, []string{"email-sent"})
	if err != nil {
		log.Panicf("Unable to add label to issue: %v", err)
	}
}

func parseCommand(command string) (string, string, string, error) {
	log.Printf("Parsing command line arguments from command: [%s]\n", command)
	commands, err := parseCommandLine(command)
	if err != nil {
		return "", "", "", fmt.Errorf("unable to parse command line arguments")
	}

	var approver, user, email string
	var approverFlag, userFlag, emailFlag bool
	switch commands[0] {
	case "/approve":
		log.Println("Identified command: [/approve]")
		if len(commands) < 8 {
			return "", "", "", fmt.Errorf("not enough arguments in command")
		}
		for i, command := range commands {
			if command == "--pm" || command == "-pm" {
				log.Println("Identified flag: [--pm]")
				approver = commands[i+1]
				approverFlag = true
			} else if command == "--name" || command == "-name" {
				log.Println("Identified flag: [--name]")
				if commands[i+2] == "--email" || commands[i+2] == "-email" {
					user = commands[i+1]
				} else {
					log.Println("Identified non-quoted name, using parse-forward to retrieve last name")
					user = fmt.Sprintf("%s %s", commands[i+1], commands[i+2])
				}
				userFlag = true
			} else if command == "--email" || command == "-email" {
				log.Println("Identified flag: [--email]")
				email = commands[i+1]
				emailFlag = true
			}
		}
	default:
		return "", "", "", fmt.Errorf("unsupported command")
	}
	if approverFlag && userFlag && emailFlag {
		if approver == "" || user == "" || email == "" {
			return "", "", "", fmt.Errorf("command contained empty flag input")
		}
		return approver, user, email, nil
	}
	return "", "", "", fmt.Errorf("required flag missing")
}

// https://stackoverflow.com/a/46973603
func parseCommandLine(command string) ([]string, error) {
	var args []string
	state := "start"
	current := ""
	quote := `""`
	escapeNext := true
	for i := 0; i < len(command); i++ {
		c := command[i]

		if state == "quotes" {
			if string(c) != quote {
				current += string(c)
			} else {
				args = append(args, current)
				current = ""
				state = "start"
			}
			continue
		}

		if escapeNext {
			current += string(c)
			escapeNext = false
			continue
		}

		if c == '\\' {
			escapeNext = true
			continue
		}

		if c == '"' || c == '\'' {
			state = "quotes"
			quote = string(c)
			continue
		}

		if state == "arg" {
			if c == ' ' || c == '\t' {
				args = append(args, current)
				current = ""
				state = "start"
			} else {
				current += string(c)
			}
			continue
		}

		if c != ' ' && c != '\t' {
			state = "arg"
			current += string(c)
		}
	}

	if state == "quotes" {
		return []string{}, errors.New(fmt.Sprintf("Unclosed quote in command line: %s", command))
	}

	if current != "" {
		args = append(args, current)
	}

	return args, nil
}
