package main

import (
	"bytes"
	"strings"

	"github.com/bwmarrin/discordgo"
)


// Execute a command
func command(msg string, m *discordgo.MessageCreate) {
	owner := m.Author.ID == OWNER
	switch(msg) {
	case "help":
		help(m)
	case "reload":
		if owner {
			reload()
		}
	}
}

// Print out all the commands
func help(m *discordgo.MessageCreate) {

	// Create a buffer
	var buffer bytes.Buffer

	// Print out collections and sounds
	buffer.WriteString("```md\n")
	for _, coll := range COLLECTIONS {
		command := PREFIX + coll.Name
		buffer.WriteString(command + "\n" + strings.Repeat("=", len(command)) + "\n")
		for _, s := range coll.Sounds {
			buffer.WriteString(s.Name + "\n")
		}
		buffer.WriteString("\n")
	}
	buffer.WriteString("```")

	// Send to channel
	discord.ChannelMessageSend(m.ChannelID, buffer.String())
}

// Reload
func reload() {
	load()
}
