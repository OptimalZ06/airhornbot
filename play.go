package main

type Play struct {
	GuildID   string
	ChannelID string
	Sounds    chan *Sound
}
