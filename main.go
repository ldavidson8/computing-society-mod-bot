package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

type ServerConfig struct {
	MemberAuditChannelID string        `json:"member_audit_channel_id"`
	UnverifiedRoleID     string        `json:"unverified_role_id"`
	RateLimitEnabled     bool          `json:"rate_limit_enabled"`
	RateLimitDuration    time.Duration `json:"rate_limit_duration"`
}

type Config struct {
	Servers map[string]ServerConfig `json:"servers"`
}

var (
	config      Config
	configMutex sync.RWMutex
)

var (
	emailRegex    = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@uclan\.ac\.uk$`)
	rateLimitMap  = make(map[string]time.Time)
	rateLimitLock sync.Mutex
)

func loadConfig() error {
	data, err := os.ReadFile("./data/config.json")
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

	err = os.MkdirAll("./data", os.ModePerm)
	if err != nil {
		return err
	}
	return os.WriteFile("./data/config.json", data, 0644)
}

var (
	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"set_member_audit_channel": setMemberAuditChannel,
		"set_unverified_role":      setUnverifiedRole,
		"enable_rate_limit":        enableRateLimit,
		"disable_rate_limit":       disableRateLimit,
		"set_rate_limit":           setRateLimit,
		"check_rate_limit":         checkRateLimit,
	}

	commands = []*discordgo.ApplicationCommand{
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
		{
			Name:        "enable_rate_limit",
			Description: "Enable rate limiting for email verification",
		},
		{
			Name:        "disable_rate_limit",
			Description: "Disable rate limiting for email verification",
		},
		{
			Name:        "set_rate_limit",
			Description: "Set the rate limit for email verification",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "minutes",
					Description: "The number of minutes to set the rate limit to",
					Required:    true,
				},
			},
		},
		{
			Name:        "check_rate_limit",
			Description: "Check the current rate limit status",
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

	// Register handlers for different interaction types
	client.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		switch i.Type {
		case discordgo.InteractionApplicationCommand:
			// Handle slash commands
			if h, ok := commandHandlers[i.ApplicationCommandData().Name]; ok {
				h(s, i)
			}
		case discordgo.InteractionMessageComponent:
			// Handle button interactions
			handleButton(s, i)
		}
	})

	// Register the messageCreate func as a callback for MessageCreate events.
	client.AddHandler(guildMemberAdd)
	client.AddHandler(memberDM)

	// Set required intents
	client.Identify.Intents = discordgo.IntentsGuildMessages |
		discordgo.IntentGuildMembers |
		discordgo.IntentDirectMessages |
		discordgo.IntentGuilds

	// Retrieve the guild ID from the .env file
	guildId := os.Getenv("GUILD_ID")
	if guildId == "" {
		log.Println("Deploying commands globally as no guild ID is provided.")
	} else {
		log.Printf("Deploying commands to guild ID: %s\n", guildId)
	}

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
	configMutex.RLock()
	serverConfig, exists := config.Servers[m.GuildID]
	configMutex.RUnlock()

	if !exists {
		log.Printf("No config found for guild %s", m.GuildID)
		return
	}

	if serverConfig.RateLimitEnabled {
		rateLimitLock.Lock()
		lastTime, exists := rateLimitMap[m.Author.ID]
		now := time.Now()
		if exists && now.Sub(lastTime) < serverConfig.RateLimitDuration {
			rateLimitLock.Unlock()
			s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Please wait %d minutes before sending another verification request.", serverConfig.RateLimitDuration/time.Minute))
			return
		}
		rateLimitMap[m.Author.ID] = now
		rateLimitLock.Unlock()
	}

	// Validate email
	if !emailRegex.MatchString(m.Content) {
		s.ChannelMessageSend(m.ChannelID, "Invalid email. Please provide a valid UCLan email.")
		return
	}

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
	serverConfig = config.Servers[guildID]
	configMutex.RUnlock()

	// Approval button
	approveButton := discordgo.Button{
		Label:    "Approve",
		Style:    discordgo.SuccessButton,
		CustomID: "approve_" + m.Author.ID,
	}

	// Denial button
	denyButton := discordgo.Button{
		Label:    "Deny",
		Style:    discordgo.DangerButton,
		CustomID: "deny_" + m.Author.ID,
	}

	// Create action row
	actionRow := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{approveButton, denyButton},
	}

	// Send verification request to member audit channel
	_, err := s.ChannelMessageSendComplex(serverConfig.MemberAuditChannelID, &discordgo.MessageSend{
		Content:    fmt.Sprintf("User %s#%s has requested verification with email %s", m.Author.Username, m.Author.Discriminator, m.Content),
		Components: []discordgo.MessageComponent{actionRow},
	})

	if err != nil {
		log.Printf("Error sending message to audit channel: %v", err)
	}
}

func handleButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		log.Printf("Error acknowledging interaction: %v", err)
		return
	}

	if i.Type != discordgo.InteractionMessageComponent {
		log.Printf("Received non-component interaction: %v", i.Type)
		return
	}

	customID := i.MessageComponentData().CustomID
	log.Printf("Received button interaction: %s", customID)

	// Split by underscore to properly separate action and userID
	parts := strings.Split(customID, "_")
	if len(parts) != 2 {
		log.Printf("Invalid button customID format: %s", customID)
		return
	}

	action := parts[0]
	userID := parts[1]

	log.Printf("Processing %s action for user %s", action, userID)

	var responseContent string
	switch action {
	case "approve":
		// Handle approval
		responseContent = fmt.Sprintf("<@%s> has been approved! Welcome to the server! 🎉", userID)

		// Send DM to the approved user
		dmChannel, err := s.UserChannelCreate(userID)
		if err != nil {
			log.Printf("Error creating DM channel: %v", err)
			return
		} else {
			approvalMessage := "You have been approved to join the UCLan Computing Society server. Welcome! 🎉"

			_, err = s.ChannelMessageSend(dmChannel.ID, approvalMessage)
			if err != nil {
				log.Printf("Error sending DM: %v", err)
			}
		}

		// Remove unverified role
		configMutex.RLock()
		if serverConfig, exists := config.Servers[i.GuildID]; exists && serverConfig.UnverifiedRoleID != "" {
			err := s.GuildMemberRoleRemove(i.GuildID, userID, serverConfig.UnverifiedRoleID)
			if err != nil {
				log.Printf("Error removing unverified role: %v", err)
			}
		}
		configMutex.RUnlock()
	case "deny":
		// Send DM to the denied user before removing them
		dmChannel, err := s.UserChannelCreate(userID)
		if err != nil {
			log.Printf("Error creating DM channel: %v", err)
			return
		} else {
			denialMessage := "Oops! You need to verify your identity with a UCLan email address to access the UCLan Computing Society server. This is to ensure only society members have access to the server and ensure we keep a safe and civil community.\n\nAs you did not verify your email, you were kicked from the server. You can rejoin and retry verification using this link: https://discord.gg/CEgCy5ejag. Thank you 🙂"

			_, err = s.ChannelMessageSend(dmChannel.ID, denialMessage)
			if err != nil {
				log.Printf("Error sending DM: %v", err)
			}
		}
		// Kick the member
		err = s.GuildMemberDelete(i.GuildID, userID)
		if err != nil {
			log.Printf("Error kicking user %s: %v", userID, err)
			errorContent := "Error processing denial"
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &errorContent,
			})
			return
		}

		responseContent = fmt.Sprintf("<@%s> has been denied and removed from the server.", userID)
	default:
		log.Printf("Unknown action: %s", action)
		unknownContent := "Unknown action"
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: &unknownContent,
		})
		return
	}

	// Update the original message to remove buttons and show the result
	_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    i.ChannelID,
		ID:         i.Message.ID,
		Content:    &responseContent,
		Components: &[]discordgo.MessageComponent{},
	})
	if err != nil {
		log.Printf("Error editing original message: %v", err)
	}

	// Edit the deferred response
	completionMessage := "Action completed successfully"
	_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &completionMessage,
	})
	if err != nil {
		log.Printf("Error editing interaction response: %v", err)
	}
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

func enableRateLimit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	configMutex.Lock()
	if _, exists := config.Servers[guildID]; !exists {
		config.Servers[guildID] = ServerConfig{}
	}

	serverConfig := config.Servers[guildID]
	serverConfig.RateLimitEnabled = true
	if serverConfig.RateLimitDuration == 0 {
		serverConfig.RateLimitDuration = 5 * time.Minute // Default to 5 minutes
	}

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
			Content: "Rate limiting enabled successfully! :white_check_mark:",
		},
	})
}

func disableRateLimit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	configMutex.Lock()
	if _, exists := config.Servers[guildID]; !exists {
		config.Servers[guildID] = ServerConfig{}
	}

	serverConfig := config.Servers[guildID]
	serverConfig.RateLimitEnabled = false
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
			Content: "Rate limiting disabled successfully! :white_check_mark:",
		},
	})
}

func setRateLimit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	minutes := options[0].IntValue()
	guildID := i.GuildID

	configMutex.Lock()
	if _, exists := config.Servers[guildID]; !exists {
		config.Servers[guildID] = ServerConfig{}
	}

	serverConfig := config.Servers[guildID]
	serverConfig.RateLimitDuration = time.Duration(minutes) * time.Minute
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
			Content: fmt.Sprintf("Rate limit set to %d minutes successfully! :white_check_mark:", minutes),
		},
	})
}

func checkRateLimit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	guildID := i.GuildID

	configMutex.RLock()
	serverConfig, exists := config.Servers[guildID]
	configMutex.RUnlock()

	if !exists {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "No rate limit configured for this server",
			},
		})
		return
	}

	var content string
	if serverConfig.RateLimitEnabled {
		content = fmt.Sprintf("Rate limit is enabled with a duration of %d minutes", int(serverConfig.RateLimitDuration/time.Minute))
	} else {
		content = "Rate limit is disabled"
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
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
	_, err = s.ChannelMessageSend(channel.ID, "Welcome! Please provide your university email for verification. For example:```example@uclan.ac.uk```")
	if err != nil {
		fmt.Println("Error sending DM:", err)
	}
}
