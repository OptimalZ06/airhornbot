package main

import (
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
)

// Add handlers for discord events
func addHandlers() {
	discord.AddHandler(onReady)
	discord.AddHandler(onMessageCreate)
	discord.AddHandler(onGuildCreate)
}

// This function will be called (due to AddHandler above) every time a new
// guild is joined.
func onGuildCreate(s *discordgo.Session, event *discordgo.GuildCreate) {
	log.Info("Guild create function has ran!")
/*
	if event.Guild.Unavailable {
		return
	}

	for _, channel := range event.Guild.Channels {
		if channel.ID == event.Guild.ID {
			_, _ = s.ChannelMessageSend(channel.ID, "Airhorn is ready! Type " + PREFIX + "airhorn while in a voice channel to play a sound.")
			return
		}
	}
	*/
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {

	// Make message lower case
	m.Content = strings.ToLower(m.Content)

	// Ignore all messages created by us
	if m.Author.ID == s.State.User.ID {

	// Get the channel
	} else if channel, _ := s.State.Channel(m.ChannelID); channel == nil {
		log.WithFields(log.Fields{
			"channel": m.ChannelID,
			"message": m.ID,
		}).Warning("Failed to grab channel")

	// No server, must be a DM
	} else if channel.GuildID == "" {
		command(m.Content, m)

	// We are being mentioned
	} else if len(m.Mentions) > 0 {
		if m.Mentions[0].ID == s.State.Ready.User.ID {
			command(strings.Trim(strings.Replace(m.ContentWithMentionsReplaced(), "@" + s.State.Ready.User.Username, "", 1), " "), m)
		}

	// Find the collection for the command we got
	} else if strings.HasPrefix(m.Content, PREFIX) {

		// Split message by spaces after prefix
		parts := strings.Split(m.Content[len(PREFIX):], " ")

		// Loop through each part and build a channel of sounds
		sounds := make(chan *Sound, MAX_CHAIN_SIZE)
		for i, plen := 0, len(parts); i < plen; {
			var (
				coll *Collection
				sound *Sound
			)

			// Skip extra spacing
			if parts[i] == "" {
				i++
				continue
			}

			// Find a collection
			for _, c := range COLLECTIONS {
				if parts[i] == c.Name {
					coll = c
					goto findSound
				}
			}
			dm(m.Author, "Could not find a sound called " + parts[i])
			return

			// Find a sound
			findSound:
			i++
			if i < plen {
				if s := coll.Find(parts[i]); s != nil {
					sound = s
					goto addSound
				}
			}
			if sound != nil {
				continue
			}

			// Add a sound
			addSound:
			if len(sounds) == MAX_CHAIN_SIZE {
				dm(m.Author, "Too many sounds requested. Limit is " + strconv.Itoa(MAX_CHAIN_SIZE) + ".")
				return
			}
			if sound != nil {
				sounds <- sound
				goto findSound
			}
			sounds <- coll.Sounds[randomRange(0, len(coll.Sounds))]
		}
		close(sounds)

		// Get the voice channel the user is in
		vc := userVoiceChannel(channel.GuildID, m.Author)
		if vc == nil {
			dm(m.Author, "Could not play the sound requested. Are you in a voice channel?")
			return
		}

		// Queue
		(&Play{
			GuildID: vc.GuildID,
			ChannelID: vc.ID,
			Sounds: sounds,
		}).enqueue()
	}
}

func onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Info("Recieved READY payload")
	s.UpdateStatus(0, "sounds")
}
