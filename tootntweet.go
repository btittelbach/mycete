package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/btittelbach/anaconda"
	twittertextextract "github.com/kylemcc/twitter-text-go/extract"
	mastodon "github.com/mattn/go-mastodon"
)

const character_limit_twitter_ int = 280
const character_limit_mastodon_ int = 500
const character_penalty_urls_ int = 23 //currently the same for twitter and mastodon. supposed to check this via twitter api at startup (low priority TODO)
const imgbytes_limit_twitter_ int64 = 5242880
const imgbytes_limit_mastodon_ int64 = 4 * 1024 * 1024

const webbaseformaturl_twitter_ string = "https://twitter.com/i/web/status/%s"

func checkCharacterLimit(status string) error {
	// get minimum character limit
	climit := 10000
	if c["server"]["mastodon"] == "true" && climit > character_limit_mastodon_ {
		climit = character_limit_mastodon_
	}
	if c["server"]["twitter"] == "true" && climit > character_limit_twitter_ {
		climit = character_limit_twitter_
	}

	//calc length as counted by twitter/mastodon
	//any URL counts as ~23 runes
	urlsinstatus := twittertextextract.ExtractUrls(status)
	statuslen := len(status)
	for _, url := range urlsinstatus {
		//echo URL is counted as a fixed number of characters
		statuslen -= len(url.Text)
		statuslen += character_penalty_urls_
	}

	//mastodon does not count screen name domains
	//find all directmsg_re_ and remove length of trailing domain
	//but only for Mastodon (aka if twitter is disabled) (shorter anyway)
	if c["server"]["mastodon"] == "true" && c["server"]["twitter"] != "true" {
		directmsg_re_ = regexp.MustCompile(`(?:^|\s)@\w+(@[a-zA-Z0-9.]+)(?:\W|$)`)
		for _, m := range directmsg_re_.FindAllStringSubmatch(status, 20) {
			statuslen -= len(m[1])
		}
	}

	// get number of characters ... this is not entirely accurate, but close enough. (read twitters API page on character counting)
	if statuslen <= climit {
		return nil
	} else {
		return fmt.Errorf("status/tweet of %d characters exceeds limit of %d", len(status), climit)
	}
}

/////////////
/// Twitter
/////////////

func initTwitterClient() *anaconda.TwitterApi {
	return anaconda.NewTwitterApiWithCredentials(
		c["twitter"]["access_token"],
		c["twitter"]["access_secret"],
		c["twitter"]["consumer_key"],
		c["twitter"]["consumer_secret"])
}

func sendTweet(client *anaconda.TwitterApi, post, matrixnick string) (weburl string, statusid int64, err error) {
	v := url.Values{}
	v.Set("status", post)
	if c.GetValueDefault("images", "enabled", "false") == "true" {
		if media_ids, _ := getImagesForTweet(client, matrixnick); media_ids != nil {
			v.Set("media_ids", strings.Join(media_ids, ","))
		}
	}
	// log.Println("sendTweet", post, v)
	var tweet anaconda.Tweet
	tweet, err = client.PostTweet(post, v)
	if err == nil {
		weburl = fmt.Sprintf(webbaseformaturl_twitter_, tweet.IdStr)
		statusid = tweet.Id
	}
	return
}

func sendTwitterDirectMessage(client *anaconda.TwitterApi, post, twitterhandle string) error {
	_, err := client.PostDMToScreenName(post, twitterhandle)
	return err
}

func getImagesForTweet(client *anaconda.TwitterApi, nick string) ([]string, error) {
	imagepaths, err := getUserFileList(nick)
	if err != nil {
		return nil, err
	}
	if len(imagepaths) == 0 {
		return nil, fmt.Errorf("No stored image for nick")
	}
	media_ids := make([]string, len(imagepaths))
	for idx, imagepath := range imagepaths {
		if b64data, err := readFileIntoBase64(imagepath); err != nil {
			return nil, err
		} else {
			if tmedia, err := client.UploadMedia(b64data); err != nil {
				return nil, err
			} else {
				media_ids[idx] = strconv.FormatInt(tmedia.MediaID, 10)
				client.AddMediaMetadata(media_ids[idx], "") // add alt_text for image
			}
		}

	}
	return media_ids, nil
}

/////////////
/// Mastodon
/////////////

func initMastodonClient() *mastodon.Client {
	return mastodon.NewClient(&mastodon.Config{
		Server:       c["mastodon"]["server"],
		ClientID:     c["mastodon"]["client_id"],
		ClientSecret: c["mastodon"]["client_secret"],
		AccessToken:  c["mastodon"]["access_token"],
	})
}

func sendToot(client *mastodon.Client, post, matrixnick string, directmsg bool, inreplyto string) (weburl string, statusid mastodon.ID, err error) {
	var mids []mastodon.ID
	usertoot := &mastodon.Toot{Status: post}
	if c.GetValueDefault("images", "enabled", "false") == "true" {
		if mids, err = getImagesForToot(client, matrixnick); err == nil {
			if mids != nil {
				usertoot.MediaIDs = mids
			}
		} else {
			log.Println("sendToot::getImagesForToot Error:", err)
		}
	}
	if directmsg {
		usertoot.Visibility = "direct"
		// usertoot.InReplyToID = TODO get last directmsg-ID IFF sender equals recipient in this post
	} else {
		usertoot.Visibility = "public"
	}
	if len(inreplyto) > 0 {
		usertoot.InReplyToID = mastodon.ID(inreplyto)
	}
	// log.Println("sendToot", usertoot)
	var mstatus *mastodon.Status
	mstatus, err = client.PostStatus(context.Background(), usertoot)
	if mstatus != nil && err == nil {
		weburl = mstatus.URL
		statusid = mstatus.ID
	}
	return
}

func uploadMediaToMastodonWithDescription(client *mastodon.Client, ctx context.Context, file string, description string) (*mastodon.Attachment, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return client.UploadMediaFromMedia(ctx, &mastodon.Media{File: f, Description: description})
}

func getImagesForToot(client *mastodon.Client, matrixnick string) ([]mastodon.ID, error) {
	imagepaths, err := getUserFileList(matrixnick)
	if err != nil {
		return nil, err
	}
	if len(imagepaths) == 0 {
		return nil, fmt.Errorf("No stored image for nick")
	}
	mastodon_ids := make([]mastodon.ID, len(imagepaths))
	for idx, imagepath := range imagepaths {
		imagedesc, imgdescerr := readDescriptionOfMediaFile(imagepath)
		if imgdescerr != nil {
			log.Println("readDescriptionOfMediaFile Error:", imgdescerr)
		}
		if attachment, err := uploadMediaToMastodonWithDescription(client, context.Background(), imagepath, imagedesc); err != nil {
			return nil, err
		} else {
			mastodon_ids[idx] = attachment.ID
		}
	}
	return mastodon_ids, nil
}
