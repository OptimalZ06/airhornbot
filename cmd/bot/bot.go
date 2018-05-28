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
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
)

var (
	// discordgo session
	discord *discordgo.Session

	// Map of Guild id's to *Play channels, used for queuing and rate-limiting guilds
	queues map[string]chan *Play = make(map[string]chan *Play)

	// Time delays
	DELAY_AFTER_SOUND = time.Millisecond * 250
	DELAY_BEFORE_SOUND = time.Millisecond * 32
	DELAY_CHANGE_CHANNEL = time.Millisecond * 250
	DELAY_JOIN_CHANNEL = time.Second * 2

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

	// The next play to occur after this, only used for chaining sounds like anotha
	Next *Play
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

	// Check if we already have a connection to this guild
	//   yes, this isn't threadsafe, but its "OK" 99% of the time
	_, exists := queues[guild.ID]

	if exists {
		if len(queues[guild.ID]) < MAX_QUEUE_SIZE {
			queues[guild.ID] <- play
		}
	} else {
		queues[guild.ID] = make(chan *Play, MAX_QUEUE_SIZE)
		playSound(play, nil)
	}
}

// Play a sound
func playSound(play *Play, vc *discordgo.VoiceConnection) (err error) {
	log.WithFields(log.Fields{
		"play": play,
	}).Info("Playing sound")

	if vc == nil {
		time.Sleep(DELAY_JOIN_CHANNEL)
		vc, err = discord.ChannelVoiceJoin(play.GuildID, play.ChannelID, false, false)
		// vc.Receive = false
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Error("Failed to play sound")
			delete(queues, play.GuildID)
			return err
		}
	}

	// If we need to change channels, do that now
	if vc.ChannelID != play.ChannelID {
		vc.ChangeChannel(play.ChannelID, false, false)
		time.Sleep(DELAY_CHANGE_CHANNEL)
	}

	// Sleep for a specified amount of time before playing the sound
	time.Sleep(DELAY_BEFORE_SOUND)

	// Play the sound
	play.Sound.Play(vc)

	// If this is chained, play the chained sound
	if play.Next != nil {
		playSound(play.Next, vc)
	}

	// If there is another song in the queue, recurse and play that
	if len(queues[play.GuildID]) > 0 {
		play := <-queues[play.GuildID]
		playSound(play, vc)
		return nil
	}

	// If the queue is empty, delete it
	time.Sleep(DELAY_AFTER_SOUND)
	delete(queues, play.GuildID)
	vc.Disconnect()
	return nil
}

func onReady(s *discordgo.Session, event *discordgo.Ready) {
	log.Info("Recieved READY payload")
	s.UpdateStatus(0, "airhornbot.com")
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if len(m.Content) <= 0 || (m.Content[0] != '!' && len(m.Mentions) < 1) {
		return
	}

	log.Info(m)

	msg := strings.Replace(m.ContentWithMentionsReplaced(), s.State.Ready.User.Username, "username", 1)
	parts := strings.Split(strings.ToLower(msg), " ")

	channel, _ := discord.State.Channel(m.ChannelID)
	if channel == nil {
		log.WithFields(log.Fields{
			"channel": m.ChannelID,
			"message": m.ID,
		}).Warning("Failed to grab channel")
		return
	}

	guild, _ := discord.State.Guild(channel.GuildID)
	if guild == nil {
		log.WithFields(log.Fields{
			"guild":   channel.GuildID,
			"channel": channel,
			"message": m.ID,
		}).Warning("Failed to grab guild")
		return
	}

	// If this is a mention, it should come from the owner (otherwise we don't care)
	if len(m.Mentions) > 0 && len(parts) > 1 {
		owner := m.Author.ID == OWNER
		for _, mention := range m.Mentions {
			if mention.ID == s.State.Ready.User.ID {
				switch(parts[1]) {
				case "help":
					help(m)
				case "reload":
					if owner {
						load()
					}
				}
			}
			break
		}

		return
	}

	// Find the collection for the command we got
	for _, coll := range COLLECTIONS {
		if parts[0] == "!" + coll.Name {

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

			go enqueuePlay(m.Author, guild, coll, sound)
			return
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
		Prefix = "!" + string(*Prefix)
		fmt.Println("Sexy Prefix: ", Prefix)
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
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	<-c
}

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
	colls := make(map[string][]string)
	for _, file := range files {

		// Only match files according to the regex below
		rp := regexp.MustCompile("^([a-z]+)_([a-z]+)\\.dca$")
		m := rp.FindAllStringSubmatch(file.Name(), -1)
		if m != nil {

			// Add to the groups collections
			coll := m[0][1]
			sound := m[0][2]
			colls[coll] = append(colls[coll], sound)
		}
	}

	// Loop through the groups collections we created and build a sound collection
	for coll, sounds := range colls {

		// Create an array of sounds
		wee := []*Sound{}
		for _, sound := range sounds {
			wee = append(wee, &Sound{
				Name:      sound,
				buffer:    make([][]byte, 0),
			})
		}

		// Append the sound collection to the collections
		COLLECTIONS = append(COLLECTIONS, &SoundCollection{
			Name: coll,
			Sounds: wee,
		})
	}

	// Preload all the sounds
	log.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
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

// This function will be called (due to AddHandler above) every time a new
// guild is joined.
func guildCreate(s *discordgo.Session, event *discordgo.GuildCreate) {
	log.Info("Guild create function has ran!")

	if event.Guild.Unavailable {
		return
	}

	for _, channel := range event.Guild.Channels {
		if channel.ID == event.Guild.ID {
			_, _ = s.ChannelMessageSend(channel.ID, "Airhorn is ready! Type !airhorn while in a voice channel to play a sound.")
			return
		}
	}
}
