package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
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
	m sync.Mutex

	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues map[string]chan *Play = make(map[string]chan *Play)

	// Time delays
	DELAY_BEFORE_DISCONNECT = time.Millisecond * 250
	DELAY_BEFORE_SOUND = time.Millisecond * 50
	DELAY_CHANGE_CHANNEL = time.Millisecond * 250
	DELAY_JOIN_CHANNEL = time.Millisecond * 200

	// Sound encoding settings
	BITRATE        = 128
	MAX_QUEUE_SIZE = 6

	// Commands prefix
	PREFIX = "!"

	// Owner
	OWNER string
)

// Play represents an individual use of the !airhorn command
type Play struct {
	GuildID   string
	ChannelID string
	UserID    string
	Sound     *Sound
}

type SoundCollection struct {
	Name    string
	Sounds    []*Sound
}

// Sound represents a sound clip
type Sound struct {
	Name string

	// Buffer to store encoded PCM packets
	buffer [][]byte
}

var COLLECTIONS []*SoundCollection = []*SoundCollection{}

func (sc *SoundCollection) Load() {
	for _, sound := range sc.Sounds {
		sound.Load(sc)
	}
}

// Load attempts to load an encoded sound file from disk
// DCA files are pre-computed sound files that are easy to send to Discord.
// If you would like to create your own DCA files, please use:
// https://github.com/nstafie/dca-rs
// eg: dca-rs --raw -i <input wav file> > <output file>
func (s *Sound) Load(c *SoundCollection) error {
	path := fmt.Sprintf("audio/%v_%v.dca", c.Name, s.Name)

	file, err := os.Open(path)

	if err != nil {
		fmt.Println("error opening dca file :", err)
		return err
	}

	var opuslen int16

	for {
		// read opus frame length from dca file
		err = binary.Read(file, binary.LittleEndian, &opuslen)

		// If this is the end of the file, just return
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}

		if err != nil {
			fmt.Println("error reading from dca file :", err)
			return err
		}

		// read encoded pcm from dca file
		InBuf := make([]byte, opuslen)
		err = binary.Read(file, binary.LittleEndian, &InBuf)

		// Should not be any end of file errors
		if err != nil {
			fmt.Println("error reading from dca file :", err)
			return err
		}

		// append encoded pcm data to the buffer
		s.buffer = append(s.buffer, InBuf)
	}
}

// Plays this sound over the specified VoiceConnection
func (s *Sound) Play(vc *discordgo.VoiceConnection) {
	vc.Speaking(true)
	defer vc.Speaking(false)

	for _, buff := range s.buffer {
		vc.OpusSend <- buff
	}
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
	rand.Seed(time.Now().UTC().UnixNano())
	return rand.Intn(max-min) + min
}

// Prepares a play
func createPlay(user *discordgo.User, guild *discordgo.Guild, coll *SoundCollection, sound *Sound) *Play {

	// Grab the users voice channel
	channel := getCurrentVoiceChannel(user, guild)
	if channel == nil {
		log.WithFields(log.Fields{
			"user":  user.ID,
			"guild": guild.ID,
		}).Warning("Failed to find channel to play sound in")
		return nil
	}

	// If we didn't get passed a manual sound, generate a random one
	if sound == nil {
		sound = coll.Sounds[randomRange(0, len(coll.Sounds))]
	}

	// Create the play
	return &Play{
		GuildID:   guild.ID,
		ChannelID: channel.ID,
		UserID:    user.ID,
		Sound:     sound,
	}
}

// Prepares and enqueues a play into the ratelimit/buffer guild queue
func enqueuePlay(user *discordgo.User, guild *discordgo.Guild, coll *SoundCollection, sound *Sound) {
	play := createPlay(user, guild, coll, sound)
	if play == nil {
		return
	}

	m.Lock()
	_, exists := queues[guild.ID]
	if exists {
		if len(queues[guild.ID]) < MAX_QUEUE_SIZE {
			queues[guild.ID] <- play
		}
	} else {
		queues[guild.ID] = make(chan *Play, MAX_QUEUE_SIZE)
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
		play.Sound.Play(vc)

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
	defer m.Unlock()
}

func onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Info("Recieved READY payload")
	s.UpdateStatus(0, "airhornbot.com")
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {

	// Ignore all messages created by us
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Get the channel
	channel, _ := discord.State.Channel(m.ChannelID)
	if channel == nil {
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
			command(strings.Trim(strings.ToLower(strings.Replace(m.ContentWithMentionsReplaced(), "@" + s.State.Ready.User.Username, "", 1)), " "), m)
		}

	// Find the collection for the command we got
	} else if strings.HasPrefix(m.Content, PREFIX) {

		// Find the server
		guild, _ := discord.State.Guild(channel.GuildID)
		if guild == nil {
			log.WithFields(log.Fields{
				"guild":   channel.GuildID,
				"channel": channel,
				"message": m.ID,
			}).Warning("Failed to grab guild")
			return
		}

		parts := strings.Split(strings.ToLower(m.Content[len(PREFIX):]), " ")
		for _, coll := range COLLECTIONS {
			if parts[0] == coll.Name {

				// If they passed a specific sound effect, find and select that (otherwise play nothing)
				var sound *Sound
				if len(parts) > 1 {
					for _, s := range coll.Sounds {
						if parts[1] == s.Name {
							sound = s
						}
					}

					if sound == nil {
						return
					}
				}

				enqueuePlay(m.Author, guild, coll, sound)
				return
			}
		}
	}
}

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

	// Register call backs
	discord.AddHandler(onReady)
	discord.AddHandler(onMessageCreate)
	discord.AddHandler(guildCreate)

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
		rp := regexp.MustCompile("^([a-z]+)_([a-z]+)\\.dca$")
		m := rp.FindAllStringSubmatch(file.Name(), -1)

		// No matches found
		if m == nil {
			continue
		}

		// Assign the coll and sound
		coll := m[0][1]
		sound := m[0][2]

		// Create and append the collection
		if collection == nil || collection.Name != coll {
			collection = &SoundCollection{
				Name: coll,
				Sounds: []*Sound{},
			}
			COLLECTIONS = append(COLLECTIONS, collection)
		}

		// Create and append the sound
		collection.Sounds = append(collection.Sounds, &Sound{
			Name: sound,
			buffer: make([][]byte, 0),
		})
	}

	// Preload all the sounds
	log.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
	}
}

// This function will be called (due to AddHandler above) every time a new
// guild is joined.
func guildCreate(s *discordgo.Session, event *discordgo.GuildCreate) {
	log.Info("Guild create function has ran!")

	if event.Guild.Unavailable {
		return
	}

	for _, channel := range event.Guild.Channels {
		if channel.ID == event.Guild.ID {
			//_, _ = s.ChannelMessageSend(channel.ID, "Airhorn is ready! Type " + PREFIX + "airhorn while in a voice channel to play a sound.")
			return
		}
	}
}
