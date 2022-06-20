package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"bufio"

	mastodon "github.com/mattn/go-mastodon"
	"github.com/matrix-org/gomatrix"
	"github.com/btittelbach/anaconda"
)

func mxNotify(mxcli *gomatrix.Client, from, to, msg string) {
	log.Printf("%s: %s\n", from, msg)

	if tonickonly, err := gomatrix.ExtractUserLocalpart(to); err == nil {
		text := fmt.Sprintf("%s: %s", tonickonly, msg)
		htmltext := fmt.Sprintf("<a href=\"https://matrix.to/#/%s\">%s</a>: %s", to, tonickonly, msg)

		mxcli.SendMessageEvent(c["matrix"]["room_id"], "m.room.message", gomatrix.HTMLMessage{MsgType: "m.text", Format: "org.matrix.custom.html", Body: text, FormattedBody: htmltext})
	} else {
		mxcli.SendText(c["matrix"]["room_id"], msg)
	}
}

func RemoveQuoteTextFromMatrixElementReplyMsg(inputbody string) (outputbody string) {
	scanner := bufio.NewScanner(strings.NewReader(inputbody))
	scanner.Split(bufio.ScanLines)
	lines_still_quoted_since_beginning_of_multiline_string := true
	for scanner.Scan() {
		line := scanner.Text()
		//find first not quoted line
		if !( strings.HasPrefix(line,"> ") || len(strings.TrimSpace(line)) == 0) {
			lines_still_quoted_since_beginning_of_multiline_string = false
		}
		//verbatim copy lines to output after quotes ended
		if !lines_still_quoted_since_beginning_of_multiline_string {
			if len(outputbody) > 0 { outputbody += "\n" }
			outputbody += line
		}
	}
	//ignore errors
	return
}

//TODO: accept strings in form:
// ✓ url (where we can detect twitter or mastodon)
// ✓ "toot <ID>" --> mastodon
// ✓ "status <ID>" --> mastdon
// ✓ "tweet <ID>" --> twitter
// ✓ "birdsite <ID>" --> twitter
// - last --> favourite the last received toot or tweet
func parseReblogFavouriteArgs(prefix, line string, mxcli *gomatrix.Client, mcmd mastodon_action_cmd, tcmd twitter_action_cmd) error {
	tort := ""
	statusidstr := ""
	args := strings.SplitN(strings.ToLower(strings.TrimSpace(line[len(prefix):])), " ", 3)
	if len(args) > 1 {
		switch args[0] {
		case "toot", "status":
			tort = mastodon_net
			statusidstr = args[1]
		case "tweet", "birdsite":
			tort = twitter_net
			statusidstr = args[1]
		}
	} else if len(args) == 1 {
		if args[0] == "last" {
			///TODO
			return fmt.Errorf("Sorry, 'last' not implemented yet")
		} else if matchlist := mastodon_status_uri_re_.FindStringSubmatch(args[0]); len(matchlist) >= 2 {
			tort = mastodon_net
			statusidstr = matchlist[1]
		} else if matchlist := twitter_status_uri_re_.FindStringSubmatch(args[0]); len(matchlist) >= 2 {
			tort = twitter_net
			statusidstr = matchlist[1]
		}
	}
	/// now execute
	switch tort {
	case twitter_net:
		return tcmd(statusidstr)
	case mastodon_net:
		return mcmd(statusidstr)
	default:
		return fmt.Errorf("Please say " + prefix + " followed by 'last', <status URL> or 'toot'/'tweet' <ID>")
	}
}

/// TODO
/// TODO Turn BotCmd's into methods of a struct with interface
/// TODO

func BotCmdReblog(mclient *mastodon.Client, tclient *anaconda.TwitterApi, rums_store_chan chan<- RUMSStoreMsg, rums_retrieve_chan chan<- RUMSRetrieveMsg, mxcli *gomatrix.Client, ev *gomatrix.Event, post string) {

	if err := parseReblogFavouriteArgs(c["matrix"]["reblog_prefix"], post, mxcli,
		func(statusid string) error {
			_, err := mclient.Reblog(context.Background(), mastodon.ID(statusid))
			if err == nil {
				rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{MatrixUser: ev.Sender, TootID: mastodon.ID(statusid), Action: actionReblog}}
			}

			return err
		},
		func(postidstr string) error {
			postid, err := strconv.ParseInt(postidstr, 10, 64)
			if err != nil {
				return err
			}
			if postid <= 0 {
				return fmt.Errorf("Sorry could not parse status id")
			}
			_, err = tclient.Retweet(postid, true)
			if err == nil {
				rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{MatrixUser: ev.Sender, TweetID: postid, Action: actionReblog}}
			}

			return err
		},
	); err == nil {
		mxNotify(mxcli, "reblog", ev.Sender, "Ok, I reblogged/retweeted that status for you")
	} else {
		mxNotify(mxcli, "reblog", ev.Sender, fmt.Sprintf("error reblogging/retweeting: %s", err.Error()))
	}
}

func BotCmdFavorite(mclient *mastodon.Client, tclient *anaconda.TwitterApi, rums_store_chan chan<- RUMSStoreMsg, rums_retrieve_chan chan<- RUMSRetrieveMsg, mxcli *gomatrix.Client, ev *gomatrix.Event, post string) {
	err := parseReblogFavouriteArgs(c["matrix"]["favourite_prefix"], post, mxcli,
		func(statusid string) error {
			_, err := mclient.Favourite(context.Background(), mastodon.ID(statusid))
			if err == nil {
				rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{MatrixUser: ev.Sender, TootID: mastodon.ID(statusid), Action: actionFav}}
			}
			return err
		},
		func(postidstr string) error {
			postid, err := strconv.ParseInt(postidstr, 10, 64)
			if err != nil {
				return err
			}
			if postid <= 0 {
				return fmt.Errorf("Sorry could not parse status id")
			}
			_, err = tclient.Favorite(postid)
			if err == nil {
				rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{MatrixUser: ev.Sender, TweetID: postid, Action: actionFav}}
			}
			return err
		},
	)
	if err == nil {
		mxNotify(mxcli, "favourite", ev.Sender, "Ok, I favourited that status for you")

	} else {
		mxNotify(mxcli, "favourite", ev.Sender, fmt.Sprintf("error favouriting: %s", err.Error()))
	}
}

func BotCmdBlogToWorld(mclient *mastodon.Client, tclient *anaconda.TwitterApi, rums_store_chan chan<- RUMSStoreMsg, rums_retrieve_chan chan<- RUMSRetrieveMsg, mxcli *gomatrix.Client, ev *gomatrix.Event, post string, markseen_c chan<- mastodon.ID) {
	lock := getPerUserLock(ev.Sender)
	lock.Lock()
	defer lock.Unlock()
	var reviewurl string
	var twitterid int64
	var mastodonid mastodon.ID
	var err error

	if c["server"]["mastodon"] == "true" {
		reviewurl, mastodonid, err = sendToot(mclient, post, ev.Sender, false, "")
		if markseen_c != nil {
			markseen_c <- mastodonid
		}
		if err != nil {
			log.Println("MastodonTootERROR:", err)
			mxNotify(mxcli, "mastodon", ev.Sender, "ERROR while tooting!")
		} else {
			mxNotify(mxcli, "mastodon", ev.Sender, fmt.Sprintf("sent toot! %s", reviewurl))
		}
	}

	if c["server"]["twitter"] == "true" {
		reviewurl, twitterid, err = sendTweet(tclient, post, ev.Sender)
		if err != nil {
			log.Println("TwitterTweetERROR:", err)
			mxNotify(mxcli, "twitter", ev.Sender, "ERROR while tweeting!")
		} else {
			mxNotify(mxcli, "twitter", ev.Sender, fmt.Sprintf("sent tweet! %s", reviewurl))
		}
	}

	//remember posted status IDs
	rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{ev.Sender, mastodonid, twitterid, actionPost}}

	//remove saved image file if present. We only attach an image once.
	if c.GetValueDefault("images", "enabled", "false") == "true" {
		rmAllUserFiles(ev.Sender)
	}
}

func BotCmdRedactStuff(mclient *mastodon.Client, tclient *anaconda.TwitterApi, rums_store_chan chan<- RUMSStoreMsg, rums_retrieve_chan chan<- RUMSRetrieveMsg, mxcli *gomatrix.Client, ev *gomatrix.Event) {

			future_chan := make(chan *MsgStatusData, 1)
			rums_retrieve_chan <- RUMSRetrieveMsg{key: ev.Redacts, future: future_chan}
			rums_ptr := <-future_chan
			if rums_ptr == nil {
				return
			}
			if c.GetValueDefault("matrix", "admins_can_redact_user_status", "false") == "true" || rums_ptr.MatrixUser == ev.Sender {
				switch rums_ptr.Action {
				case actionPost:
					if rums_ptr.TweetID > 0 {
						if _, err := tclient.DeleteTweet(rums_ptr.TweetID, true); err == nil {
							mxNotify(mxcli, "redaction", ev.Sender, "Ok, I deleted that tweet for you")
						} else {
							log.Println("RedactTweetERROR:", err)
							mxNotify(mxcli, "redaction", ev.Sender, "Could not redact your tweet")
						}
					}
					if len(rums_ptr.TootID) > 0 {
						if err := mclient.DeleteStatus(context.Background(), rums_ptr.TootID); err == nil {
							mxNotify(mxcli, "redaction", ev.Sender, "Ok, I deleted that toot for you")
						} else {
							log.Println("RedactTweetERROR", err)
							mxNotify(mxcli, "redaction", ev.Sender, "Could not redact your toot")
						}
					}
				case actionReblog:
					if rums_ptr.TweetID > 0 {
						if _, err := tclient.UnRetweet(rums_ptr.TweetID, true); err == nil {
							mxNotify(mxcli, "redaction", ev.Sender, "Ok, I un-retweetet that tweet for you")
						} else {
							log.Println("RedactTweetERROR:", err)
							mxNotify(mxcli, "redaction", ev.Sender, "Could not redact your retweet")
						}
					}
					if len(rums_ptr.TootID) > 0 {
						if _, err := mclient.Unreblog(context.Background(), rums_ptr.TootID); err == nil {
							mxNotify(mxcli, "redaction", ev.Sender, "Ok, I un-reblogged that toot for you")
						} else {
							log.Println("RedactTweetERROR", err)
							mxNotify(mxcli, "redaction", ev.Sender, "Could not redact your reblog")
						}
					}
				case actionFav:
					if rums_ptr.TweetID > 0 {
						if _, err := tclient.Unfavorite(rums_ptr.TweetID); err == nil {
							mxNotify(mxcli, "redaction", ev.Sender, "Ok, I removed your favor from that tweet")
						} else {
							log.Println("RedactTweetERROR:", err)
							mxNotify(mxcli, "redaction", ev.Sender, "Could not redact your favor")
						}
					}
					if len(rums_ptr.TootID) > 0 {
						if _, err := mclient.Unfavourite(context.Background(), rums_ptr.TootID); err == nil {
							mxNotify(mxcli, "redaction", ev.Sender, "Ok, I removed your favour from that toot")
						} else {
							log.Println("RedactTweetERROR", err)
							mxNotify(mxcli, "redaction", ev.Sender, "Could not redact your favour")
						}
					}

				}
			} else {
				mxNotify(mxcli, "redaction", ev.Sender, "Won't redact other users status for you! Set admins_can_redact_user_status=true if you disagree.")
			}

}