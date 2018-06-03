package main

import (
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
)

var (
	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues map[string]chan *Play = make(map[string]chan *Play)

	// Mutex
	m sync.Mutex
)

type Play struct {
	GuildID   string
	ChannelID string
	Sounds    chan *Sound
}

// Prepares and enqueues a play into the ratelimit/buffer guild queue
func (p *Play) enqueue() {
	m.Lock()
	if _, ok := queues[p.GuildID]; ok {
		if len(queues[p.GuildID]) < MAX_QUEUE_SIZE {
			queues[p.GuildID] <- p
		}
	} else {
		queues[p.GuildID] = make(chan *Play, MAX_QUEUE_SIZE)
		go p.play(nil)
	}
	m.Unlock()
}

// Play a sound
func (p *Play) play(vc *discordgo.VoiceConnection) {
	log.WithFields(log.Fields{
		"play": p,
	}).Info("Playing sound")

	// Create channel
	if vc == nil {
		time.Sleep(DELAY_JOIN_CHANNEL)
		var err error
		vc, err = discord.ChannelVoiceJoin(p.GuildID, p.ChannelID, false, false)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Error("Failed to play sound")
			vc = nil
		}

	// Change channel
	} else if vc.ChannelID != p.ChannelID {
		time.Sleep(DELAY_CHANGE_CHANNEL)
		vc.ChangeChannel(p.ChannelID, false, false)
	}

	// If we have a connection
	if vc != nil {

		// Play the sound
		time.Sleep(DELAY_BEFORE_SOUND)
		for sound := range p.Sounds {
			time.Sleep(DELAY_BEFORE_SOUND_CHAIN)
			sound.Play(vc)
		}

		// Disconnect if queue is empty
		if len(queues[p.GuildID]) == 0 {
			time.Sleep(DELAY_BEFORE_DISCONNECT)
			vc.Disconnect()
			vc = nil
		}
	}

	// Lock
	m.Lock()

	// Keep playing
	if len(queues[p.GuildID]) > 0 {
		defer (<-queues[p.GuildID]).play(vc)

	// Delete the queue
	} else {
		delete(queues, p.GuildID)
	}

	// Unlock
	m.Unlock()
}
