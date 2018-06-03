package main

import (
	"bytes"
	"flag"
	"io/ioutil"
	"math/rand"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
)

var (
	// discordgo session
	discord *discordgo.Session

	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues map[string]chan *Play = make(map[string]chan *Play)

	// Mutex
	m sync.Mutex

	// Time delays
	DELAY_BEFORE_DISCONNECT = time.Millisecond * 250
	DELAY_BEFORE_SOUND = time.Millisecond * 50
	DELAY_BEFORE_SOUND_CHAIN = time.Millisecond * 25
	DELAY_CHANGE_CHANNEL = time.Millisecond * 250
	DELAY_JOIN_CHANNEL = time.Millisecond * 175

	// Collections
	COLLECTIONS []*SoundCollection = []*SoundCollection{}

	// Commands prefix
	PREFIX = "!"

	// Owner
	OWNER string
)

// Limits
const (
	MAX_CHAIN_SIZE = 3
	MAX_QUEUE_SIZE = 6
)

func main() {
	var (
		Token      = flag.String("t", "", "Discord Authentication Token")
		Shard      = flag.String("s", "", "Shard ID")
		ShardCount = flag.String("c", "", "Number of shards")
		Owner      = flag.String("o", "", "Owner ID")
		Prefix		 = flag.String("p", "", "Prefix for commands")
		err        error
	)
	flag.Parse()

	if *Owner != "" {
		OWNER = *Owner
	}

	if *Prefix != "" {
		PREFIX = *Prefix
		log.Info("Custom prefix has been set to: ", PREFIX)
	}

	// Create a new random seed
	rand.Seed(time.Now().UTC().UnixNano())

	// Load all sounds and build collections
	load()

	// Create a discord session
	log.Info("Starting discord session...")
	discord, err = discordgo.New("Bot " + *Token)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Fatal("Failed to create discord session")
		return
	}

	// Set sharding info
	discord.ShardID, _ = strconv.Atoi(*Shard)
	discord.ShardCount, _ = strconv.Atoi(*ShardCount)
	if discord.ShardCount <= 0 {
		discord.ShardCount = 1
	}

	// Add handlers
	addHandlers()

	err = discord.Open()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err,
		}).Fatal("Failed to create discord websocket connection")
		return
	}

	// We're running!
	log.Info("AIRHORNBOT is ready to horn it up.")

	// Wait for a signal to quit
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	discord.Close()
}

// Attempts to find the current users voice channel inside a given guild
func getCurrentVoiceChannel(user *discordgo.User, guild *discordgo.Guild) *discordgo.Channel {
	for _, vs := range guild.VoiceStates {
		if vs.UserID == user.ID {
			channel, _ := discord.State.Channel(vs.ChannelID)
			return channel
		}
	}
	return nil
}

// Returns a random integer between min and max
func randomRange(min, max int) int {
	return rand.Intn(max-min) + min
}


// Prepares and enqueues a play into the ratelimit/buffer guild queue
func enqueuePlay(play *Play) {
	m.Lock()
	if _, ok := queues[play.GuildID]; ok {
		if len(queues[play.GuildID]) < MAX_QUEUE_SIZE {
			queues[play.GuildID] <- play
		}
	} else {
		queues[play.GuildID] = make(chan *Play, MAX_QUEUE_SIZE)
		go playSound(play, nil)
	}
	m.Unlock()
}

// Play a sound
func playSound(play *Play, vc *discordgo.VoiceConnection) {
	log.WithFields(log.Fields{
		"play": play,
	}).Info("Playing sound")

	// Create channel
	if vc == nil {
		time.Sleep(DELAY_JOIN_CHANNEL)
		var err error
		vc, err = discord.ChannelVoiceJoin(play.GuildID, play.ChannelID, false, false)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Error("Failed to play sound")
			vc = nil
		}

	// Change channel
	} else if vc.ChannelID != play.ChannelID {
		time.Sleep(DELAY_CHANGE_CHANNEL)
		vc.ChangeChannel(play.ChannelID, false, false)
	}

	// If we have a connection
	if vc != nil {

		// Play the sound
		time.Sleep(DELAY_BEFORE_SOUND)
		for sound := range play.Sounds {
			time.Sleep(DELAY_BEFORE_SOUND_CHAIN)
			sound.Play(vc)
		}

		// Disconnect if queue is empty
		if len(queues[play.GuildID]) == 0 {
			time.Sleep(DELAY_BEFORE_DISCONNECT)
			vc.Disconnect()
			vc = nil
		}
	}

	// Lock
	m.Lock()

	// Keep playing
	if len(queues[play.GuildID]) > 0 {
		play := <-queues[play.GuildID]
		defer playSound(play, vc)

	// Delete the queue
	} else {
		delete(queues, play.GuildID)
	}

	// Unlock
	m.Unlock()
}

// Execute a command
func command(msg string, m *discordgo.MessageCreate) {
	owner := m.Author.ID == OWNER
	switch(msg) {
	case "help":
		help(m)
	case "reload":
		if owner {
			load()
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

// Load collections and sounds from file
func load() {
	log.Info("Loading files and building collections")

	// Reset the collections
	COLLECTIONS = []*SoundCollection{}

	// Read all files from the audio directory
	files, err := ioutil.ReadDir("audio")
	if err != nil {
		log.Fatal(err)
	}

	// Loop through each file and store into a collections map
	var collection *SoundCollection
	for _, file := range files {

		// Only match files according to the regex below
		r := regexp.MustCompile("^([a-z]+)_([a-z]+)\\.dca$")

		// Match found
		if m := r.FindStringSubmatch(file.Name()); m != nil {

			// Create and append the collection
			if collection == nil || collection.Name != m[1] {
				collection = &SoundCollection{
					Name: m[1],
					Sounds: []*Sound{},
				}
				COLLECTIONS = append(COLLECTIONS, collection)
			}

			// Create and append the sound
			collection.Sounds = append(collection.Sounds, &Sound{
				Name: m[2],
				buffer: make([][]byte, 0),
			})
		}
	}

	// Preload all the sounds
	log.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
	}
}
