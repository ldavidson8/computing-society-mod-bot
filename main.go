package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

type ServerConfig struct {
	VerificationChannelID string `json:"verification_channel_id"`
	MemberAuditChannelID  string `json:"member_audit_channel_id"`
	UnverifiedRoleID      string `json:"unverified_role_id"`
}

type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

var (
	config      Config
	configMutex sync.RWMutex
)

func loadConfig() error {
	data, err := os.ReadFile("config.json")
	if err != nil {
		if os.IsNotExist(err) {
			config = Config{Servers: make(map[string]ServerConfig)}
			return nil
		}
		return err
	}
	configMutex.Lock()
	defer configMutex.Unlock()
	return json.Unmarshal(data, &config)
}

func saveConfig() error {
	configMutex.RLock()
	defer configMutex.RUnlock()
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile("config.json", data, 0644)
}

var (
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"set_verification_channel": setVerificationChannel,
		"set_member_audit_channel": setMemberAuditChannel,
		"set_unverified_role":      setUnverifiedRole,
	}

	commands = []*discordgo.ApplicationCommand{
		{
			Name:        "set_verification_channel",
			Description: "Set the verification channel",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to set as the verification channel",
					Required:    true,
				},
			},
		},
		{
			Name:        "set_member_audit_channel",
			Description: "Set the member audit channel",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to use for member audits",
					Required:    true,
				},
			},
		},
		{
			Name:        "set_unverified_role",
			Description: "Set the unverified role",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionRole,
					Name:        "role",
					Description: "The role to set as the unverified role",
					Required:    true,
				},
			},
		},
	}
)

func main() {
	// Recover from panics
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Bot crashed: %v", r)
		}
	}()

	// Load config
	err := loadConfig()
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
		// If the config doesn't exist, it's not a fatal error
	}

	// Load .env file
	err = godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}
	log.Println("Successfully loaded .env file")

	// Get the token from the .env file
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("No token provided. Please set DISCORD_BOT_TOKEN in .env")
	}
	log.Println("Successfully retrieved bot token")

	// Create a new Discord session using the provided bot token.
	client, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}
	log.Println("Successfully created Discord session")

	// Register the messageCreate func as a callback for MessageCreate events.
	client.AddHandler(guildMemberAdd)

	// Handle slash commands
	client.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
			h(s, i)
		}
	})

	// Register the messageCreate func as a callback for MessageCreate events.
	client.AddHandler(memberDM)

	// Retrieve the guild ID from the .env file
	guildId := os.Getenv("GUILD_ID")
	if guildId == "" {
		log.Println("Deploying commands globally as no guild ID is provided.")
	} else {
		log.Printf("Deploying commands to guild ID: %s\n", guildId)
	}

	client.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentGuildMembers | discordgo.IntentDirectMessages | discordgo.IntentGuilds

	// Open a websocket connection to Discord and begin listening.
	err = client.Open()
	if err != nil {
		log.Fatalf("Error opening connection: %v", err)
		return
	}

	// Register slash commands
	registeredCommands, err := client.ApplicationCommandBulkOverwrite(client.State.User.ID, guildId, commands)
	if err != nil {
		log.Fatalf("Error registering slash commands: %v", err)
	}
	log.Printf("Registered %d commands", len(registeredCommands))

	log.Println("Bot is now running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	log.Println("Shutting down...")
	client.Close()
}

func memberDM(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore all messages created by the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Check if the message is a DM
	channel, err := s.Channel(m.ChannelID)
	if err != nil {
		fmt.Println("Error getting channel:", err)
		return
	}

	if channel.Type == discordgo.ChannelTypeDM {
		// Process email verification
		processEmailVerification(s, m)
		return
	}
}

func processEmailVerification(s *discordgo.Session, m *discordgo.MessageCreate) {
	// TODO: Implement email verification logic

	// Find the guild ID for this user
	guilds := s.State.Guilds
	var guildID string
	for _, guild := range guilds {
		member, err := s.GuildMember(guild.ID, m.Author.ID)
		if err == nil && member != nil {
			guildID = guild.ID
			break
		}
	}

	if guildID == "" {
		log.Println("User is not in any guild")
		return
	}

	configMutex.RLock()
	serverConfig, exists := config.Servers[guildID]
	configMutex.RUnlock()

	if !exists || serverConfig.VerificationChannelID == "" {
		log.Printf("No member audit channel configured for guild %s", guildID)
		return
	}

	// Send verification request to member audit channel
	_, err := s.ChannelMessageSend(serverConfig.MemberAuditChannelID, fmt.Sprintf("User %s has requested verification with email %s", m.Author.ID, m.Content))
	if err != nil {
		log.Printf("Error sending message to audit channel: %v", err)
	}
}

func setVerificationChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	channelID := options[0].ChannelValue(s).ID
	guildID := i.GuildID

	configMutex.Lock()
	if _, exists := config.Servers[guildID]; !exists {
		config.Servers[guildID] = ServerConfig{}
	}
	serverConfig := config.Servers[guildID]
	serverConfig.VerificationChannelID = channelID
	config.Servers[guildID] = serverConfig
	configMutex.Unlock()

	err := saveConfig()
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error saving config: " + err.Error(),
			},
		})
		return
	}

	// TODO: Save channelId to config

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Verification channel set successfully! :white_check_mark: <#%s>", channelID),
		},
	})
}

func setMemberAuditChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	channelID := options[0].ChannelValue(s).ID
	guildID := i.GuildID

	configMutex.Lock()
	if _, exists := config.Servers[guildID]; !exists {
		config.Servers[guildID] = ServerConfig{}
	}
	serverConfig := config.Servers[guildID]
	serverConfig.MemberAuditChannelID = channelID
	config.Servers[guildID] = serverConfig
	configMutex.Unlock()

	err := saveConfig()
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error saving config: " + err.Error(),
			},
		})
		return
	}

	// TODO: Save channelId to config

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Member audit channel set successfully! :white_check_mark: <#%s>", channelID),
		},
	})
}

func setUnverifiedRole(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	roleID := options[0].RoleValue(s, i.GuildID).ID
	guildID := i.GuildID

	configMutex.Lock()
	if _, exists := config.Servers[guildID]; !exists {
		config.Servers[guildID] = ServerConfig{}
	}
	serverConfig := config.Servers[guildID]
	serverConfig.UnverifiedRoleID = roleID
	config.Servers[guildID] = serverConfig
	configMutex.Unlock()

	err := saveConfig()
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error saving config: " + err.Error(),
			},
		})
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Verified role set successfully! :white_check_mark: <@&%s>", roleID),
		},
	})
}

func guildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	configMutex.RLock()
	serverConfig, exists := config.Servers[m.GuildID]
	configMutex.RUnlock()

	if !exists {
		log.Printf("No config found for guild %s", m.GuildID)
		return
	}

	// Apply Unverified role
	if serverConfig.UnverifiedRoleID != "" {
		err := s.GuildMemberRoleAdd(m.GuildID, m.User.ID, serverConfig.UnverifiedRoleID)
		if err != nil {
			log.Printf("Error adding role to user %s: %v", m.User.ID, err)
		}
	} else {
		log.Printf("No unverified role configured for guild %s", m.GuildID)
	}

	// Send DM to new member
	channel, err := s.UserChannelCreate(m.User.ID)
	if err != nil {
		fmt.Printf("Error creating DM channel: %v\n", err)
		return
	}

	_, err = s.ChannelMessageSend(channel.ID, "Welcome! Please provide your university email for verification.")
	if err != nil {
		fmt.Println("Error sending DM:", err)
	}
}
