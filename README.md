# Computing Society Mod Bot

This is a Discord bot designed for managing and moderating a Discord server for a computing society. The bot is built using the [discordgo](https://github.com/bwmarrin/discordgo) library.

## Features

- Responds to direct messages
- Handles guild messages
- Registers and handles slash commands

## Prerequisites

- Go 1.16 or higher
- A Discord bot token
- A `.env` file with the following content:
  ```env
  DISCORD_TOKEN=your_discord_bot_token
  ```

# Setup

1. Clone the repository:

   ```sh
   git clone https://github.com/ldavidson8/computing-society-mod-bot.git
   cd computing-society-mod-bot
   ```

2. Install dependencies:

   ```sh
   go mod tidy
   ```

3. Create a `.env` file in the root directory and add your Discord bot token:

   ```env
   DISCORD_TOKEN=your_discord_bot_token
   ```

4. Run the bot:
   ```sh
   go run main.go
   ```

## Usage

Once the bot is running, invite it to your Discord server using the OAuth2 URL with the appropriate permissions. The bot will start responding to messages and handling commands as configured.

## License

This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.
