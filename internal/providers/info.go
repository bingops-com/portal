package providers

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apognu/gocal"
	"github.com/mmcdole/gofeed"
)

// --- RSS ---------------------------------------------------------------

type feedItem struct {
	Title     string     `json:"title"`
	Link      string     `json:"link"`
	Source    string     `json:"source"`
	Published *time.Time `json:"published,omitempty"`
}

type feedResult struct {
	Items  []feedItem `json:"items"`
	Failed []string   `json:"failed,omitempty"`
}

func parseFeed(ctx context.Context, rawurl string) (*gofeed.Feed, error) {
	body, err := getBytes(ctx, rawurl)
	if err != nil {
		return nil, err
	}
	feed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		return nil, fmt.Errorf("flux illisible sur %s", redact(rawurl))
	}
	return feed, nil
}

func itemTime(it *gofeed.Item) *time.Time {
	if it.PublishedParsed != nil {
		return it.PublishedParsed
	}
	return it.UpdatedParsed
}

func fetchRSS(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Feeds []namedURL `json:"feeds"`
		Limit int        `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Feeds) == 0 {
		return nil, errors.New("ajoutez au moins un flux dans les réglages du widget")
	}
	perFeed := make([][]feedItem, len(o.Feeds))
	failed := make([]string, len(o.Feeds))
	parallel(len(o.Feeds), 6, func(i int) {
		f := o.Feeds[i]
		feed, err := parseFeed(ctx, f.URL)
		if err != nil {
			failed[i] = err.Error()
			return
		}
		source := f.Title
		if source == "" {
			source = feed.Title
		}
		for _, it := range feed.Items {
			perFeed[i] = append(perFeed[i], feedItem{
				Title: strings.TrimSpace(html.UnescapeString(it.Title)), Link: it.Link, Source: source, Published: itemTime(it),
			})
		}
	})
	res := feedResult{Items: []feedItem{}}
	for i := range perFeed {
		res.Items = append(res.Items, perFeed[i]...)
		if failed[i] != "" {
			res.Failed = append(res.Failed, failed[i])
		}
	}
	if len(res.Items) == 0 && len(res.Failed) > 0 {
		return nil, errors.New(res.Failed[0])
	}
	sort.SliceStable(res.Items, func(a, b int) bool {
		ta, tb := res.Items[a].Published, res.Items[b].Published
		if ta == nil || tb == nil {
			return ta != nil
		}
		return ta.After(*tb)
	})
	if limit := clamp(o.Limit, 12, 1, 60); len(res.Items) > limit {
		res.Items = res.Items[:limit]
	}
	return res, nil
}

// --- YouTube -----------------------------------------------------------

type video struct {
	Title     string     `json:"title"`
	Link      string     `json:"link"`
	Channel   string     `json:"channel"`
	Thumbnail string     `json:"thumbnail"`
	Published *time.Time `json:"published,omitempty"`
}

var channelIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{10,40}$`)

func fetchVideos(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Channels []string `json:"channels"`
		Limit    int      `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	if len(o.Channels) == 0 {
		return nil, errors.New("ajoutez au moins un identifiant de chaîne YouTube (UC…)")
	}
	perChannel := make([][]video, len(o.Channels))
	errs := make([]error, len(o.Channels))
	parallel(len(o.Channels), 6, func(i int) {
		id := strings.TrimSpace(o.Channels[i])
		if !channelIDRe.MatchString(id) {
			return
		}
		feed, err := parseFeed(ctx, "https://www.youtube.com/feeds/videos.xml?channel_id="+id)
		if err != nil {
			errs[i] = err
			return
		}
		for _, it := range feed.Items {
			vid := ""
			if yt, ok := it.Extensions["yt"]; ok && len(yt["videoId"]) > 0 {
				vid = yt["videoId"][0].Value
			}
			if vid == "" || strings.Contains(it.Link, "/shorts/") {
				continue
			}
			perChannel[i] = append(perChannel[i], video{
				Title: it.Title, Link: it.Link, Channel: feed.Title, Published: itemTime(it),
				Thumbnail: "https://i.ytimg.com/vi/" + vid + "/mqdefault.jpg",
			})
		}
	})
	videos := []video{}
	for _, v := range perChannel {
		videos = append(videos, v...)
	}
	if len(videos) == 0 {
		if err := errors.Join(errs...); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(videos, func(a, b int) bool {
		ta, tb := videos[a].Published, videos[b].Published
		if ta == nil || tb == nil {
			return ta != nil
		}
		return ta.After(*tb)
	})
	if limit := clamp(o.Limit, 8, 1, 40); len(videos) > limit {
		videos = videos[:limit]
	}
	return map[string]any{"videos": videos}, nil
}

// --- Crypto markets ----------------------------------------------------

type coin struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Symbol    string    `json:"symbol"`
	Image     string    `json:"image"`
	Price     float64   `json:"price"`
	Change24h float64   `json:"change24h"`
	Sparkline []float64 `json:"sparkline"`
}

var coinIDRe = regexp.MustCompile(`^[a-z0-9-]{1,60}$`)

func fetchMarkets(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Coins    []string `json:"coins"`
		Currency string   `json:"currency"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	currency := strings.ToLower(o.Currency)
	if !regexp.MustCompile(`^[a-z]{3,5}$`).MatchString(currency) {
		currency = "usd"
	}
	var ids []string
	for _, c := range o.Coins {
		if c = strings.ToLower(strings.TrimSpace(c)); coinIDRe.MatchString(c) {
			ids = append(ids, c)
		}
	}
	if len(ids) == 0 {
		return nil, errors.New("ajoutez au moins un identifiant CoinGecko (bitcoin, ethereum…)")
	}
	var raw []struct {
		ID     string  `json:"id"`
		Symbol string  `json:"symbol"`
		Name   string  `json:"name"`
		Image  string  `json:"image"`
		Price  float64 `json:"current_price"`
		Change float64 `json:"price_change_percentage_24h"`
		Spark  struct {
			Price []float64 `json:"price"`
		} `json:"sparkline_in_7d"`
	}
	q := url.Values{"vs_currency": {currency}, "ids": {strings.Join(ids, ",")}, "sparkline": {"true"}, "price_change_percentage": {"24h"}}
	if err := getJSON(ctx, "https://api.coingecko.com/api/v3/coins/markets?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	byID := map[string]coin{}
	for _, r := range raw {
		byID[r.ID] = coin{ID: r.ID, Name: r.Name, Symbol: strings.ToUpper(r.Symbol), Image: r.Image, Price: r.Price, Change24h: r.Change, Sparkline: downsample(r.Spark.Price, 48)}
	}
	coins := []coin{}
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			coins = append(coins, c)
		}
	}
	return map[string]any{"currency": currency, "coins": coins}, nil
}

func downsample(in []float64, n int) []float64 {
	if len(in) <= n {
		return in
	}
	out := make([]float64, n)
	for i := range out {
		out[i] = in[i*(len(in)-1)/(n-1)]
	}
	return out
}

// --- Weather -----------------------------------------------------------

func fetchWeather(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Location  string   `json:"location"`
		Latitude  *float64 `json:"latitude"`
		Longitude *float64 `json:"longitude"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	name := o.Location
	var lat, lon float64
	switch {
	case o.Latitude != nil && o.Longitude != nil:
		lat, lon = *o.Latitude, *o.Longitude
	case o.Location != "":
		var geo struct {
			Results []struct {
				Name      string  `json:"name"`
				Country   string  `json:"country"`
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"results"`
		}
		q := url.Values{"name": {o.Location}, "count": {"1"}, "language": {"fr"}}
		if err := getJSON(ctx, "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode(), &geo); err != nil {
			return nil, err
		}
		if len(geo.Results) == 0 {
			return nil, fmt.Errorf("ville introuvable: %q", o.Location)
		}
		lat, lon, name = geo.Results[0].Latitude, geo.Results[0].Longitude, geo.Results[0].Name
	default:
		return nil, errors.New("indiquez une ville dans les réglages du widget")
	}

	var raw struct {
		Current struct {
			Time     string  `json:"time"`
			Temp     float64 `json:"temperature_2m"`
			Feels    float64 `json:"apparent_temperature"`
			Humidity float64 `json:"relative_humidity_2m"`
			Code     int     `json:"weather_code"`
			Wind     float64 `json:"wind_speed_10m"`
			IsDay    int     `json:"is_day"`
		} `json:"current"`
		Hourly struct {
			Time   []string  `json:"time"`
			Temp   []float64 `json:"temperature_2m"`
			Precip []float64 `json:"precipitation_probability"`
			Code   []int     `json:"weather_code"`
		} `json:"hourly"`
		Daily struct {
			Time   []string  `json:"time"`
			Code   []int     `json:"weather_code"`
			Max    []float64 `json:"temperature_2m_max"`
			Min    []float64 `json:"temperature_2m_min"`
			Precip []float64 `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	q := url.Values{
		"latitude":      {strconv.FormatFloat(lat, 'f', 4, 64)},
		"longitude":     {strconv.FormatFloat(lon, 'f', 4, 64)},
		"current":       {"temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m,is_day"},
		"hourly":        {"temperature_2m,precipitation_probability,weather_code"},
		"daily":         {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max"},
		"timezone":      {"auto"},
		"forecast_days": {"6"},
	}
	if err := getJSON(ctx, "https://api.open-meteo.com/v1/forecast?"+q.Encode(), &raw); err != nil {
		return nil, err
	}

	type hour struct {
		Time   string  `json:"time"`
		Temp   float64 `json:"temp"`
		Precip float64 `json:"precip"`
		Code   int     `json:"code"`
	}
	type day struct {
		Date   string  `json:"date"`
		Code   int     `json:"code"`
		Max    float64 `json:"max"`
		Min    float64 `json:"min"`
		Precip float64 `json:"precip"`
	}
	hours := []hour{}
	h := raw.Hourly
	nowHour := raw.Current.Time
	if len(nowHour) >= 13 {
		nowHour = nowHour[:13]
	}
	for i := range h.Time {
		if len(hours) == 24 || i >= len(h.Temp) || i >= len(h.Precip) || i >= len(h.Code) {
			break
		}
		if len(h.Time[i]) >= 13 && h.Time[i][:13] >= nowHour {
			hours = append(hours, hour{h.Time[i], h.Temp[i], h.Precip[i], h.Code[i]})
		}
	}
	days := []day{}
	d := raw.Daily
	for i := range d.Time {
		if i >= len(d.Code) || i >= len(d.Max) || i >= len(d.Min) || i >= len(d.Precip) {
			break
		}
		days = append(days, day{d.Time[i], d.Code[i], d.Max[i], d.Min[i], d.Precip[i]})
	}
	return map[string]any{
		"location": name,
		"current": map[string]any{
			"temp": raw.Current.Temp, "feels": raw.Current.Feels, "humidity": raw.Current.Humidity,
			"code": raw.Current.Code, "wind": raw.Current.Wind, "isDay": raw.Current.IsDay == 1,
		},
		"hourly": hours,
		"daily":  days,
	}, nil
}

// --- Calendar (iCal) ---------------------------------------------------

type calEvent struct {
	Title    string    `json:"title"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	AllDay   bool      `json:"allDay"`
	Location string    `json:"location,omitempty"`
	Calendar string    `json:"calendar,omitempty"`
}

func fetchCalendar(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Calendars []namedURL `json:"calendars"`
		Days      int        `json:"days"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	end := now.AddDate(0, 0, clamp(o.Days, 45, 7, 120))

	perCal := make([][]calEvent, len(o.Calendars))
	failed := make([]string, len(o.Calendars))
	parallel(len(o.Calendars), 4, func(i int) {
		c := o.Calendars[i]
		if c.URL == "" {
			return
		}
		body, err := getBytes(ctx, strings.Replace(c.URL, "webcal://", "https://", 1))
		if err != nil {
			failed[i] = err.Error()
			return
		}
		parser := gocal.NewParser(strings.NewReader(string(body)))
		parser.Start, parser.End = &start, &end
		if err := parser.Parse(); err != nil {
			failed[i] = "calendrier illisible sur " + redact(c.URL)
			return
		}
		for _, e := range parser.Events {
			if e.Start == nil {
				continue
			}
			ev := calEvent{Title: e.Summary, Start: *e.Start, End: *e.Start, Location: e.Location, Calendar: c.Title}
			if e.End != nil {
				ev.End = *e.End
			}
			ev.AllDay = e.RawStart.Params["VALUE"] == "DATE"
			perCal[i] = append(perCal[i], ev)
		}
	})
	events := []calEvent{}
	var errs []string
	for i := range perCal {
		events = append(events, perCal[i]...)
		if failed[i] != "" {
			errs = append(errs, failed[i])
		}
	}
	if len(events) == 0 && len(errs) > 0 {
		return nil, errors.New(errs[0])
	}
	sort.SliceStable(events, func(a, b int) bool { return events[a].Start.Before(events[b].Start) })
	if len(events) > 400 {
		events = events[:400]
	}
	return map[string]any{"events": events, "failed": errs}, nil
}

// --- Hacker News / Reddit ----------------------------------------------

type post struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	CommentsURL string    `json:"commentsUrl"`
	Points      int       `json:"points"`
	Comments    int       `json:"comments"`
	Created     time.Time `json:"created"`
	Source      string    `json:"source,omitempty"`
}

func fetchHackerNews(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Limit int `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	var raw struct {
		Hits []struct {
			Title    string    `json:"title"`
			URL      string    `json:"url"`
			Points   int       `json:"points"`
			Comments int       `json:"num_comments"`
			Created  time.Time `json:"created_at"`
			ID       string    `json:"objectID"`
		} `json:"hits"`
	}
	limit := clamp(o.Limit, 10, 1, 30)
	if err := getJSON(ctx, fmt.Sprintf("https://hn.algolia.com/api/v1/search?tags=front_page&hitsPerPage=%d", limit), &raw); err != nil {
		return nil, err
	}
	posts := []post{}
	for _, h := range raw.Hits {
		p := post{Title: h.Title, URL: h.URL, Points: h.Points, Comments: h.Comments, Created: h.Created,
			CommentsURL: "https://news.ycombinator.com/item?id=" + h.ID}
		if p.URL == "" {
			p.URL = p.CommentsURL
		}
		posts = append(posts, p)
	}
	sort.SliceStable(posts, func(a, b int) bool { return posts[a].Points > posts[b].Points })
	return map[string]any{"posts": posts, "scores": true}, nil
}

var subredditRe = regexp.MustCompile(`^[A-Za-z0-9_]{2,30}$`)

func fetchReddit(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Subreddits []string `json:"subreddits"`
		Sort       string   `json:"sort"`
		Limit      int      `json:"limit"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	var subs []string
	for _, s := range o.Subreddits {
		if s = strings.TrimPrefix(strings.TrimSpace(s), "r/"); subredditRe.MatchString(s) {
			subs = append(subs, s)
		}
	}
	if len(subs) == 0 {
		return nil, errors.New("ajoutez au moins un subreddit (selfhosted, kubernetes…)")
	}
	sortBy := "hot"
	if o.Sort == "top" || o.Sort == "new" {
		sortBy = o.Sort
	}
	limit := clamp(o.Limit, 10, 1, 30)
	// Reddit refuses anonymous JSON requests but still serves Atom feeds; one
	// multi-subreddit feed keeps us well under its rate limit. Feeds carry no
	// scores, so posts are listed in Reddit's own order.
	u := fmt.Sprintf("https://www.reddit.com/r/%s/%s.rss?limit=%d&t=day", strings.Join(subs, "+"), sortBy, limit+4)
	feed, err := parseFeed(ctx, u)
	if err != nil {
		if strings.Contains(err.Error(), fmt.Sprint(http.StatusForbidden)) || strings.Contains(err.Error(), fmt.Sprint(http.StatusTooManyRequests)) {
			return nil, errors.New("Reddit limite les requêtes anonymes depuis cette adresse, nouvel essai dans quelques minutes")
		}
		return nil, err
	}
	posts := []post{}
	for _, it := range feed.Items {
		if len(posts) == limit {
			break
		}
		p := post{Title: html.UnescapeString(it.Title), URL: it.Link, CommentsURL: it.Link}
		if t := itemTime(it); t != nil {
			p.Created = *t
		}
		if len(it.Categories) > 0 {
			p.Source = "r/" + it.Categories[0]
		}
		posts = append(posts, p)
	}
	return map[string]any{"posts": posts, "scores": false}, nil
}

// --- Bookmarks with optional health checks -----------------------------

type bookmark struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	CheckURL    string `json:"checkUrl,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"` // up | down
	Millis      int64  `json:"ms,omitempty"`
}

type bookmarkGroup struct {
	Title string     `json:"title"`
	Links []bookmark `json:"links"`
}

func fetchBookmarks(ctx context.Context, _ *Deps, opts map[string]any) (any, error) {
	var o struct {
		Groups []bookmarkGroup `json:"groups"`
	}
	if err := decode(opts, &o); err != nil {
		return nil, err
	}
	type ref struct{ g, l int }
	var checks []ref
	for g := range o.Groups {
		for l := range o.Groups[g].Links {
			if o.Groups[g].Links[l].CheckURL != "" {
				checks = append(checks, ref{g, l})
			}
		}
	}
	parallel(len(checks), 8, func(i int) {
		b := &o.Groups[checks[i].g].Links[checks[i].l]
		b.Status = "down"
		if checkURL(b.CheckURL) != nil {
			return
		}
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, b.CheckURL, nil)
		if err != nil {
			return
		}
		req.Header.Set("User-Agent", userAgent)
		started := time.Now()
		resp, err := httpClient.Do(req)
		if err != nil {
			return
		}
		resp.Body.Close()
		b.Millis = time.Since(started).Milliseconds()
		if resp.StatusCode < 400 {
			b.Status = "up"
		}
	})
	// In-cluster check addresses are of no use to the browser.
	for g := range o.Groups {
		for l := range o.Groups[g].Links {
			o.Groups[g].Links[l].CheckURL = ""
		}
	}
	if o.Groups == nil {
		o.Groups = []bookmarkGroup{}
	}
	return map[string]any{"groups": o.Groups}, nil
}
