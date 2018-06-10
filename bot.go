package main

import (
	"flag"
	"io/ioutil"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/bwmarrin/discordgo"
)

var (
	// Collections
	COLLECTIONS []*Collection

	// Random sounds
	RANDOM []string

	// Commands prefix
	PREFIX = "!"

	// Owner
	OWNER string
)
const (

	// Time delays
	DELAY_BEFORE_DISCONNECT = time.Millisecond * 250
	DELAY_BEFORE_SOUND = time.Millisecond * 50
	DELAY_BEFORE_SOUND_CHAIN = time.Millisecond * 25
	DELAY_CHANGE_CHANNEL = time.Millisecond * 250
	DELAY_JOIN_CHANNEL = time.Millisecond * 175

	// Limits
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

	// Load all sounds and build collections
	load()

	// Create a discord session
	log.Info("Starting discord session boi...")
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

	// Open Discord session
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

	// Close Discord session.
	discord.Close()
}

// Load collections and sounds from file
func load() {
	log.Info("Loading files and building collections")

	// Reset the collections and random... is random needed here?
	COLLECTIONS = []*Collection{}
	RANDOM = []string{}

	// Read all files from the audio directory
	files, err := ioutil.ReadDir("audio")
	if err != nil {
		log.Fatal(err)
	}

	// Loop through each file and store into a collections map
	// Also storing each file name for random selection command
	// Create temp rand command and parent
	var (
		collection *Collection
		parent string
		rand string
	)

	for _, file := range files {

		// Only match files according to the regex below
		r := regexp.MustCompile("^([a-z]+)_([a-z]+)\\.dca$")

		// Match found
		if m := r.FindStringSubmatch(file.Name()); m != nil {

			// Create and append the collection
			if collection == nil || collection.Name != m[1] {
				collection = &Collection{
					Name: m[1],
					Sounds: []*Sound{},
				}
				COLLECTIONS = append(COLLECTIONS, collection)
				parent = m[1]
			}

			// Create and append the sound
			collection.Sounds = append(collection.Sounds, &Sound{
				Name: m[2],
				buffer: make([][]byte, 0),
			})

			// Append sound name to RANDOM
			rand = parent + " " + m[2]
			RANDOM = append(RANDOM, rand)
		}
	}

	// Preload all the sounds
	log.Info("Preloading sounds...")
	for _, coll := range COLLECTIONS {
		coll.Load()
	}
}
