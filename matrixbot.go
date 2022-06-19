package main

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	mastodon "github.com/mattn/go-mastodon"
	"github.com/matrix-org/gomatrix"
)

const (
	twitter_net  string = "twitter"
	mastodon_net string = "mastodon"
)

var (
	mastodon_status_uri_re_ *regexp.Regexp
	twitter_status_uri_re_  *regexp.Regexp
	directmsg_re_           *regexp.Regexp
)

func init() {
	mastodon_status_uri_re_ = regexp.MustCompile(`^https?://[^/]+/(?:@\w+|web/statuses)/(\d+)$`)
	twitter_status_uri_re_ = regexp.MustCompile(`^https?://(?:mobile\.)?twitter\.com/.+/status(?:es)?/(\d+)$`)
	directmsg_re_ = regexp.MustCompile(`(?:^|\s)(@\w+(?:@[a-zA-Z0-9.]+)?)(?:\W|$)`)
}

// Ignore messages from ourselves
// Ignore messages from rooms we are not interessted in
func mxIgnoreEvent(ev *gomatrix.Event) bool {
	return ev.Sender == c["matrix"]["user"] || ev.RoomID != c["matrix"]["room_id"]
}

type mastodon_action_cmd func(string) error
type twitter_action_cmd func(string) error


func runMatrixPublishBot() {
	mxcli, _ := gomatrix.NewClient(c["matrix"]["url"], "", "")
	resp, err := mxcli.Login(&gomatrix.ReqLogin{
		Type:     "m.login.password",
		User:     c["matrix"]["user"],
		Password: c["matrix"]["password"],
	})

	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	mclient := initMastodonClient()
	tclient := initTwitterClient()

	mxcli.SetCredentials(resp.UserID, resp.AccessToken)

	rums_store_chan, rums_retrieve_chan := runRememberUsersMessageToStatus()

	if _, err := mxcli.JoinRoom(c["matrix"]["room_id"], "", nil); err != nil {
		panic(err)
	}

	var markseen_c chan<- mastodon.ID = nil
	if c.SectionInConfig("feed2matrix") {
		markseen_c = taskWriteMastodonBackIntoMatrixRooms(mclient, mxcli)
	}

	syncer := mxcli.Syncer.(*gomatrix.DefaultSyncer)
	syncer.OnEventType("m.room.message", func(ev *gomatrix.Event) {
		if mxIgnoreEvent(ev) { //ignore messages from ourselves or from other rooms in case of dual-login
			return
		}

		if mtype, ok := ev.MessageType(); ok {
			switch mtype {
			case "m.text":
				if post, ok := ev.Body(); ok {
					log.Printf("Message: '%s'", post)

					if rel_to_i, rel_to_inmap := ev.Content["m.relates_to"]; rel_to_inmap {
						if in_reply_to, in_reply_to_inmap := rel_to_i.(map[string]interface{})["m.in_reply_to"]; in_reply_to_inmap {
							if reply_to_event_id_i, reply_to_event_id_inmap := in_reply_to.(map[string]interface{})["event_id"]; reply_to_event_id_inmap {

								go func() {
									// check the type of message the reply-to event_id was
									reply_to_event_id := reply_to_event_id_i.(string)
									futuremsg := make(chan *MsgStatusData)
									rums_retrieve_chan <- RUMSRetrieveMsg{key: reply_to_event_id, future: futuremsg}
									reply_to_msg_data := <- futuremsg
									if nil != reply_to_msg_data {
										if ev.Sender != reply_to_msg_data.MatrixUser {
											log.Println("Reply to Message: User", ev.Sender, "is not", reply_to_msg_data.MatrixUser)
											return										
										}

										//our action depend on what kind of event that was
										switch reply_to_msg_data.Action {
											case actionPost:
												// do nothing in case of our own post
											case actionReblog:
												// do nothing if we reblogged
											case actionFav:
												// do nothing if we fav'ed
											case actionMedia:
												// add description to media
												err = saveMediaFileDescription(ev.Sender, reply_to_event_id, strings.TrimSpace(post))
												if err != nil {
													errmsg := fmt.Sprintf("Error saving description: %s", err)
													mxNotify(mxcli, "imgdesc", ev.Sender, errmsg)
													log.Println(errmsg)
												} else {
													mxNotify(mxcli, "imgdesc", ev.Sender, fmt.Sprintf("I attached your description to the image"))	
												}
											case actionMediaDesc:
												//do nothing
											default:
												//do nothing
										}
									}
								}()
							}
						}
					}

					if strings.HasPrefix(post, c["matrix"]["reblog_prefix"]) {
						/// CMD Reblogging

						go BotCmdReblog(mclient, tclient, rums_store_chan, rums_retrieve_chan, mxcli, ev, post)
						
					} else if strings.HasPrefix(post, c["matrix"]["favourite_prefix"]) {
						/// CMD Favourite

						go BotCmdFavorite(mclient, tclient, rums_store_chan, rums_retrieve_chan, mxcli, ev, post)
						
					} else if strings.HasPrefix(post, c["matrix"]["directtweet_prefix"]) {
						/// CMD Twitter Direct Message

						if c["server"]["twitter"] != "true" {
							return
						}

						post = strings.TrimSpace(post[len(c["matrix"]["directtweet_prefix"]):])

						if len(post) > character_limit_twitter_ {
							log.Println("Direct Tweet too long")
							mxNotify(mxcli, "directtweet", ev.Sender, fmt.Sprintf("Not direct-tweeting this! Too long"))
							return
						}

						m := directmsg_re_.FindStringSubmatch(post)
						if len(m) < 2 {
							mxNotify(mxcli, "directtweet", ev.Sender, "No can do! A direct message requires a recepient. Please mention an @screenname.")
							return
						}

						go func() {
							for _, rcpt := range m[1:] {
								err := sendTwitterDirectMessage(tclient, post, rcpt)
								if err != nil {
									mxNotify(mxcli, "directtweet", ev.Sender, fmt.Sprintf("Error Twitter-direct-messaging %s: %s", rcpt, err.Error()))
								}
							}
						}()

					} else if strings.HasPrefix(post, c["matrix"]["directtoot_prefix"]) || strings.HasPrefix(post, c["matrix"]["tootreply_prefix"]) {
						/// CMD Mastodon Direct Toot

						log.Println("direct toot or reply")

						if c["server"]["mastodon"] != "true" {
							return
						}

						var inreplyto string
						var private bool

						if strings.HasPrefix(post, c["matrix"]["directtoot_prefix"]) {
							post = strings.TrimSpace(post[len(c["matrix"]["directtoot_prefix"]):])
							private = true
						} else {
							post = strings.TrimSpace(post[len(c["matrix"]["tootreply_prefix"]):])
							private = false
						}

						arglist := strings.SplitN(post, " ", 2)
						if arglist != nil && len(arglist) == 2 {
							matchlist := mastodon_status_uri_re_.FindStringSubmatch(strings.TrimSpace(arglist[0]))
							if len(matchlist) >= 2 {
								inreplyto = matchlist[1]
								post = strings.TrimSpace(arglist[1])
							}
						}

						if len(post) > character_limit_mastodon_ {
							log.Println("Direct Toot too long")
							mxNotify(mxcli, "directtoot", ev.Sender, "Not tooting this! Too long")
							return
						}

						if directmsg_re_.MatchString(post) == false {
							mxNotify(mxcli, "directtoot", ev.Sender, "No can do! A direct message requires a recepient. Please mention an @username.")
							return
						}

						go func() {
							lock := getPerUserLock(ev.Sender)
							lock.Lock()
							defer lock.Unlock()
							var reviewurl string
							var mastodonid mastodon.ID

							reviewurl, mastodonid, err = sendToot(mclient, post, ev.Sender, private, inreplyto)
							if markseen_c != nil {
								markseen_c <- mastodonid
							}
							if err != nil {
								log.Println("MastodonTootERROR:", err)
								mxNotify(mxcli, "mastodon", ev.Sender, "ERROR while tooting!")
							} else {
								mxNotify(mxcli, "mastodon", ev.Sender, fmt.Sprintf("sent toot! %s", reviewurl))
							}

							//remember posted status IDs
							rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{ev.Sender, mastodonid, 0, actionPost}}

							//remove saved image file if present. We only attach an image once.
							if c.GetValueDefault("images", "enabled", "false") == "true" {
								rmAllUserFiles(ev.Sender)
							}

						}()

					} else if strings.HasPrefix(post, c["matrix"]["guard_prefix"]) {
						/// CMD Posting

						post = strings.TrimSpace(post[len(c["matrix"]["guard_prefix"]):])

						if err = checkCharacterLimit(post); err != nil {
							log.Println(err)
							mxNotify(mxcli, "limitcheck", ev.Sender, fmt.Sprintf("Not tweeting/tooting this! %s", err.Error()))
							return
						}

						go BotCmdBlogToWorld(mclient, tclient, rums_store_chan, rums_retrieve_chan, mxcli, ev, post, markseen_c)


					// } else if strings.HasPrefix(post, c["matrix"]["mediadesc_prefix"]) {
					// 	/// CMD Posting

					// 	post = strings.TrimSpace(post[len(c["matrix"]["mediadesc_prefix"]):])

					// 	if c.GetValueDefault("images", "enabled", "false") != "true" {
					// 		mxNotify(mxcli, "error", ev.Sender, "image support is disabled. Set [images]enabled=true")
					// 		return							
					// 	}

					// 	if err = checkCharacterLimit(post); err != nil {
					// 		log.Println(err)
					// 		mxNotify(mxcli, "limitcheck", ev.Sender, fmt.Sprintf("Media description too long! %s", err.Error()))
					// 		return
					// 	}

					// 	go func() {
					// 		lock := getPerUserLock(ev.Sender)
					// 		lock.Lock()
					// 		defer lock.Unlock()
					// 		var reviewurl string
					// 		var twitterid int64
					// 		var mastodonid mastodon.ID

					// 		//// TODO: add description to last queued image
					//		//// use func addMediaFileDescriptionToLastMediaUpload(nick, description string) error
					// 	}()

					} else if strings.HasPrefix(post, c["matrix"]["help_prefix"]) {
						/// CMD Help

						mxNotify(mxcli, "helptext", "", strings.Join([]string{
							"List of available command prefixes:",
							c["matrix"]["guard_prefix"] + " This text following the prefix at start of this line would be tweeted and tooted",
							c["matrix"]["directtoot_prefix"] + " [toot url] This text following would be tooted privately @user if at least one @user is contained in this line. Optionally in reply to a [toot url] given at the start.",
							c["matrix"]["tootreply_prefix"] + " <toot url> This will publicly reply to a given toot. Only works in-instance for now.",
							c["matrix"]["directtweet_prefix"] + " Buggy and does not work",
							c["matrix"]["reblog_prefix"] + " <toot url | twitter url> will be reblogged or retweeted",
							c["matrix"]["favourite_prefix"] + " <toot url | twitter url> will be favourited",
						}, "\n"))
					}
				}
			case "m.image":
				if c.GetValueDefault("images", "enabled", "false") != "true" {
					mxNotify(mxcli, "error", ev.Sender, "image support is disabled. Set [images]enabled=true")
					fmt.Println("ignoring image since support not enabled in config file")
					return
				}
				if infomapi, inmap := ev.Content["info"]; inmap {
					if infomap, ok := infomapi.(map[string]interface{}); ok {
						if imgsizei, insubmap := infomap["size"]; insubmap {
							if imgsize, ok2 := imgsizei.(int64); ok2 {
								if err = checkImageBytesizeLimit(imgsize); err != nil {
									mxNotify(mxcli, "imagesaver", ev.Sender, err.Error())
									return
								}
							}
						}
					}
				}
				if urli, inmap := ev.Content["url"]; inmap {
					if url, ok := urli.(string); ok {
						go func() {
							lock := getPerUserLock(ev.Sender)
							lock.Lock()
							defer lock.Unlock()
							if err := saveMatrixFile(mxcli, ev.Sender, ev.ID, url); err != nil {
								mxNotify(mxcli, "error", ev.Sender, "Could not get your image! "+err.Error())
								fmt.Println("ERROR downloading image:", err)
								return
							}
							// save event id of saved image, so we know where to attach description in case of reply
							rums_store_chan <- RUMSStoreMsg{key: ev.ID, data: MsgStatusData{MatrixUser: ev.Sender, Action: actionMedia}}
							// notify user
							mxNotify(mxcli, "imagesaver", ev.Sender, fmt.Sprintf("image saved. Will tweet/toot with %s's next message", ev.Sender))
						}()
					}
				}
			case "m.video", "m.audio":
				fmt.Printf("%s messages are currently not supported", mtype)
				mxNotify(mxcli, "runMatrixPublishBot", ev.Sender, "Ahh. Audio/Video files are not supported directly. Please just include it's URL in your Toot/Tweet and Mastodon/Twitter will do the rest.")
			default:
				fmt.Printf("%s messages are currently not supported", mtype)
			}
		}
	})

	/// Support redactions to "take back an uploaded image" or "delete a toot/tweet"
	syncer.OnEventType("m.room.redaction", func(ev *gomatrix.Event) {
		if mxIgnoreEvent(ev) { //ignore messages from ourselves or from other rooms in case of dual-login
			return
		}
		if c.GetValueDefault("images", "enabled", "false") == "true" {
			go func() {
				lock := getPerUserLock(ev.Sender)
				lock.Lock()
				defer lock.Unlock()
				err := rmFile(ev.Sender, ev.Redacts)
				if err == nil {
					mxNotify(mxcli, "redaction", ev.Sender, fmt.Sprintf("%s's image has been redacted. Next toot/weet will not contain that image.", ev.Sender))
				}
				if err != nil && !os.IsNotExist(err) {
					log.Println("ERROR deleting image:", err)
				}

			}()
		}
		
		go BotCmdRedactStuff(mclient, tclient, rums_store_chan, rums_retrieve_chan, mxcli, ev)

	})

	/// Send a warning or welcome text to newly joined users
	if len(c.GetValueDefault("matrix", "join_welcome_text", "")) > 0 {
		syncer.OnEventType("m.room.member", func(ev *gomatrix.Event) {
			if mxIgnoreEvent(ev) { //ignore messages from ourselves or from other rooms in case of dual-login
				return
			}

			if membership, inmap := ev.Content["membership"]; inmap && membership == "join" {
				mxNotify(mxcli, "welcomer", ev.Sender, c["matrix"]["join_welcome_text"])
			}
		})
	}

	/// Inform typing users that they might have forgotten some uploaded images
	if c.GetValueDefault("images", "enabled", "false") == "true" {
		syncer.OnEventType("m.typing", func(ev *gomatrix.Event) {
			if mxIgnoreEvent(ev) { //ignore messages from ourselves or from other rooms in case of dual-login
				return
			}

			typing_user_list_interface := ev.Content["user_ids"]
			for _, userid_i := range typing_user_list_interface.([]interface{}) {
				if userid, ok := userid_i.(string); ok {
					user_filelist, err := getUserFileList(userid)
					if err != nil {
						log.Println("Error getting Filelist of ", userid, " due to: ", err)
						return
					}
					if len(user_filelist) > 0 {
						warnmsg := c.GetValueDefault("feed2matrix", "image_timeout_warning", "Warning! There are old images ready to send. Better check before tweeting/tooting!")
						_, outdated_filelist, err := filterFilelistByFileAge(user_filelist, feed2matrx_image_timeout_)
						if err != nil {
							log.Println("Error filtering Filelist of ", userid, " by age due to: ", err)
							return
						}
						if len(outdated_filelist) > 0 {
							mxNotify(mxcli, "warnoldimages", userid, warnmsg)
						}
					}
				}
			}
		})
	}

	///run event loop
	for {
		log.Println("syncing..")
		if err := mxcli.Sync(); err != nil {
			fmt.Println("Sync() returned ", err)
		}
		time.Sleep(100 * time.Second)
	}
}
