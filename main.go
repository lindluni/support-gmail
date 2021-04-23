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

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
)

// Retrieve a token, saves the token, then returns the generated client.
func getClient(config *oauth2.Config) *http.Client {
	tokFile := "token.json"
	tok, err := tokenFromFile()
	if err != nil {
		tok = getTokenFromWeb(config)
		saveToken(tokFile, tok)
	}
	return config.Client(context.Background(), tok)
}

// Request a token from the web, then returns the retrieved token.
func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the "+
		"authorization code: \n%v\n", authURL)

	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	tok, err := config.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

// Retrieves a token from a local file.
func tokenFromFile() (*oauth2.Token, error) {
	token := os.Getenv("INPUT_TOKEN")
	tok := &oauth2.Token{}
	err := json.Unmarshal([]byte(token), tok)
	return tok, err
}

// Saves a token to a file path.
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

type Email struct {
	FromName  string
	FromEmail string
	ToName    string
	ToEmail   string
	Subject   string
	Message   string
}

func main() {
	creds := os.Getenv("INPUT_CREDENTIALS")

	// If modifying these scopes, delete your previously saved token.json.
	config, err := google.ConfigFromJSON([]byte(creds), gmail.GmailSendScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(config)

	srv, err := gmail.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve Gmail client: %v", err)
	}

	approverEmail, userName, userEmail, err := parseCommand(os.Getenv("INPUT_COMMAND"))
	if err != nil {
		panic(err)
	}

	em := &Email{
		FromName:  "GitHub",
		FromEmail: os.Getenv("INPUT_FROM"),
		ToName:    "PM/COR",
		ToEmail:   approverEmail,
		Subject:   "User Access Request",
		Message:   fmt.Sprintf(os.Getenv("INPUT_TEMPLATE"), userName, userEmail),
	}
	from := mail.Address{Name: em.FromName, Address: em.FromEmail}
	to := mail.Address{Name: em.ToName, Address: em.ToEmail}

	header := make(map[string]string)
	header["From"] = from.String()
	header["To"] = to.String()
	if !strings.Contains(os.Getenv("INPUT_COMMAND"), "skip") {
		header["cc"] = os.Getenv("INPUT_FROM")
	}
	header["Subject"] = em.Subject
	header["MIME-Version"] = "1.0"
	header["Content-Type"] = `text/plain; charset="utf-8"`

	var msg string
	for k, v := range header {
		msg += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	msg += "\r\n" + em.Message

	gmsg := gmail.Message{
		Raw: base64.RawURLEncoding.EncodeToString([]byte(msg)),
	}

	_, err = srv.Users.Messages.Send("me", &gmsg).Do()
	if err != nil {
		panic(err)
	}

}

func parseCommand(command string) (string, string, string, error) {
	var approver, user, email string
	var approverFlag, userFlag, emailFlag bool
	commands, err := parseCommandLine(command)
	if err != nil {
		panic(err)
	}
	switch commands[0] {
	case "/approve":
		if len(commands) < 8 {
			return "", "", "", fmt.Errorf("not enough arguments in command")
		}
		for i, command := range commands {
			if command == "--pm" || command == "-pm" {
				approver = commands[i+1]
				approverFlag = true
			} else if command == "--name" || command == "-name" {
				if commands[i+2] == "--email" || commands[i+2] == "-email" {
					user = commands[i+1]
				} else {
					user = fmt.Sprintf("%s %s", commands[i+1], commands[i+2])
				}
				userFlag = true
			} else if command == "--email" || command == "-email" {
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
