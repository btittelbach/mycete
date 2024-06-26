package main

import (
	"fmt"
	"html"
	"regexp"
	"strings"

	mastodon "github.com/mattn/go-mastodon"
	"github.com/microcosm-cc/bluemonday"
)

func getLocalTootUrlFromLocalID(id mastodon.ID) string {
	return c.GetValueDefault("mastodon", "server", "https://mastodon.social") + "/web/statuses/" + string(id)
}

func formatUserNameForMatrix(acct mastodon.Account) (dn string, h string) {
	tagstripper := bluemonday.StrictPolicy()
	username := strings.TrimSpace(html.UnescapeString(tagstripper.Sanitize(acct.Username)))
	displayname := strings.TrimSpace(html.UnescapeString(tagstripper.Sanitize(acct.DisplayName)))
	sender1 := strings.TrimSpace(html.UnescapeString(tagstripper.Sanitize(acct.Acct)))
	dn = "@" + sender1
	if len(username) > 0 {
		h = dn
		dn = "@" + username
	}
	if len(displayname) > 0 {
		h = dn
		dn = displayname
	}
	return
}

func sanitizeFormatStatusForMatrix(status *mastodon.Status) (url, body, htmlbody string) {
	tagstripper := bluemonday.NewPolicy()
	tagstripper.AllowElements("br")
	tagstripper_html := bluemonday.NewPolicy()
	tagstripper_html.AllowElements("br", "strike", "em", "i", "b", "strong", "code", "tt", "p")
	re_br2newline := regexp.MustCompile("<br[^/>]*/?>")

	htmlbody = tagstripper_html.Sanitize(status.Content)
	body = html.UnescapeString(strings.TrimSpace(re_br2newline.ReplaceAllString(tagstripper.Sanitize(status.Content), "\n")))
	url = status.URL

	if len(body) > matrix_notice_character_limit_ {
		body = body[0:matrix_notice_character_limit_] + "..."
	}

	return
}

func formatLocalReplyUrlHint(status *mastodon.Status) (foreignreplyhint_text, foreignreplyhint_html string) {
	if status == nil {
		return
	}
	if len(status.URL) > 0 && !hasStringMatchingPrefix(strings.ToLower(status.URL), strings.ToLower(c.GetValueDefault("mastodon", "server", "https://mastodon.social"))) {
		localurl := getLocalTootUrlFromLocalID(status.ID)
		foreignreplyhint_text = fmt.Sprintf("\n( %s publicy or %s privatly, using %s )", c["matrix"]["tootreply_prefix"], c["matrix"]["directtoot_prefix"], localurl)
		foreignreplyhint_html = fmt.Sprintf("<br/>( %s publicy or %s privatly, using <a href=\"%s\">%s</a> )", c["matrix"]["tootreply_prefix"], c["matrix"]["directtoot_prefix"], localurl, localurl)
	}
	return
}

func formatStatusForMatrix(status *mastodon.Status) (body, htmlbody string) {
	sender, handle := formatUserNameForMatrix(status.Account)
	url, body, htmlbody := sanitizeFormatStatusForMatrix(status)
	foreignreplyhint_text, foreignreplyhint_html := formatLocalReplyUrlHint(status)

	body = fmt.Sprintf("%s (%s) [ %s ]>\n%s%s", sender, handle, url, body, foreignreplyhint_text)
	htmlbody = fmt.Sprintf("<u><strong>%s</strong> (%s) writes in <a href=\"%s\">%s</a>&gt;</u><br/>%s%s", sender, handle, url, url, htmlbody, foreignreplyhint_html)
	return
}

func formatNotificationForMatrix(notification *mastodon.Notification) (body, htmlbody string) {
	sender, handle := formatUserNameForMatrix(notification.Account)
	var content_text string
	var content_html string
	var url string
	var visibility string
	if notification.Status != nil {
		url, content_text, content_html = sanitizeFormatStatusForMatrix(notification.Status)
		visibility = notification.Status.Visibility
	}
	foreignreplyhint_text, foreignreplyhint_html := formatLocalReplyUrlHint(notification.Status)

	switch notification.Type {
	case "mention":

		body = fmt.Sprintf("%s (%s) mentioned you in %s status [ %s ]:\n%s%s", sender, handle, visibility, url, content_text, foreignreplyhint_text)
		htmlbody = fmt.Sprintf("<u><strong>%s</strong> (%s) mentioned you in %s status <a href=\"%s\">%s</a>&gt;</u><br/>%s%s", sender, handle, visibility, url, url, content_html, foreignreplyhint_html)
	case "reblog":
		body = fmt.Sprintf("%s (%s) reblogged your status [ %s ]%s", sender, handle, url, foreignreplyhint_text)
		htmlbody = fmt.Sprintf("<strong>%s</strong> (%s) reblogged your status <a href=\"%s\">%s</a>%s", sender, handle, url, url, foreignreplyhint_html)
	case "favourite":
		body = fmt.Sprintf("%s (%s) favourited your status [ %s ]", sender, handle, url)
		htmlbody = fmt.Sprintf("<strong>%s</strong> (%s) favourited your status <a href=\"%s\">%s</a>", sender, handle, url, url)
	case "follow":
		body = fmt.Sprintf("%s (%s) is following you now", sender, handle)
		htmlbody = fmt.Sprintf("<strong>%s</strong> (%s) is following you now", sender, handle)
	case "follow_request":
		body = fmt.Sprintf("%s (%s) would like to follow you!", sender, handle)
		htmlbody = fmt.Sprintf("<strong>%s</strong> (%s) would like to follow you!", sender, handle)
	case "poll":
		body = fmt.Sprintf("the result of %s's poll is in: %s%s", sender, url, foreignreplyhint_text)
		htmlbody = fmt.Sprintf("the result of <strong>%s</strong>'s poll is in: <a href=\"%s\">%s</a>%s", sender, url, url, foreignreplyhint_html)
	default:
		body = fmt.Sprintf("received unsupported notification of type %s from %s (%s)%s", notification.Type, sender, handle, foreignreplyhint_text)
		htmlbody = fmt.Sprintf("received unsupported notification of type %s from %s (%s)%s", notification.Type, sender, handle, foreignreplyhint_html)
	}
	return
}
