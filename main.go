package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)



func main() {
	// Load .env file
	err := godotenv.Load()
	if err != nil {
		fmt.Println("Error loading .env file")
	}

	// Retrieve the bot token from the .env file
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		fmt.Println("No token provided. Please set DISCORD_BOT_TOKEN in .env")
		return
	}

	// Create a new Discord session using the provided bot token.
	client, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("error creating Discord session,", err)
		return
	}

	// Register the messageCreate func as a callback for MessageCreate events.
	client.AddHandler(messageCreate)

	client.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentMessageContent

	// Open a websocket connection to Discord and begin listening.
	err = client.Open()
	if err != nil {
		fmt.Println("error opening connection,", err)
		return
	}

	// Wait here until CTRL-C or other term signal is received.
	fmt.Println("Bot is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Cleanly close down the Discord session.
	client.Close()
}

func messageCreate (s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore all messages created by the bot itself
    if m.Author.ID == s.State.User.ID {
        return
    }

    // If the message is "ping" reply with "Pong!"
    if m.Content == "ping" {
        s.ChannelMessageSend(m.ChannelID, "Pong!")
    }

	if m.Content == "pong" {
		s.ChannelMessageSend(m.ChannelID, "Ping!")
	}
}