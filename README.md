# The only reason this is working is because of [arosolino](https://github.com/arosolino).
Thanks bud. You da real mvp.

# Airhorn Bot
Airhorn bot utilizes the [discordgo](https://github.com/bwmarrin/discordgo) library, a free and open source library.

## Usage
Airhorn Bot is now only a bot client that handles the playing of loyal airhorns. Once added to your server, airhorn bot can be summoned by running `!airhorn`.


### Running the Bot

**Prerequisites:**
Go 1.4 or higher

**First install the bot:** Like on a server or maybe a pi if you're cool enough.
```
go get github.com/calebjessie/airhornbot/cmd/bot
go install github.com/calebjessie/airhornbot/cmd/bot
```
 **Then run the following command:**

```
bot -r "localhost:6379" -t "MY_BOT_ACCOUNT_TOKEN" -o OWNER_ID
```

## Thanks to the original devs
Thanks to the awesome (one might describe them as smart... loyal... appreciative...) [iopred](https://github.com/iopred) and [bwmarrin](https://github.com/bwmarrin/discordgo) for helping code review the initial release.


# If you have a broken Airhornbot
Here's what I did to fix it for the new API updates.

1. Make sure this is import is all lowercase. Broke some stuff on my ubuntu server. If you go [here](github.com/sirupsen/logrus) you'll see why.

```
log "github.com/sirupsen/logrus"
```
