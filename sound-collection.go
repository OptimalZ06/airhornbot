package main

type SoundCollection struct {
	Name    string
	Sounds    []*Sound
}

func (sc *SoundCollection) Load() {
	for _, sound := range sc.Sounds {
		sound.Load(sc)
	}
}
