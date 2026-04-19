package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

type newsFetchTool struct{}

type newsFetchArgs struct {
	Region     string   `json:"region"`
	Topic      string   `json:"topic"`
	Language   string   `json:"language"`
	Sources    []string `json:"sources"`
	MaxItems   int      `json:"max_items"`
	FreshHours int      `json:"fresh_hours"`
	Fresh      bool     `json:"fresh"`
}

type newsItem struct {
	Headline    string `json:"headline"`
	Source      string `json:"source"`
	URL         string `json:"url,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Section     string `json:"section,omitempty"`
	Region      string `json:"region,omitempty"`
	Language    string `json:"language,omitempty"`
	Locality    string `json:"locality,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
	FetchMethod string `json:"fetch_method,omitempty"`
}

type newsFailure struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	Error  string `json:"error"`
	Status int    `json:"status,omitempty"`
}

type newsFetchOutput struct {
	GeneratedAt string        `json:"generated_at"`
	Region      string        `json:"region,omitempty"`
	Topic       string        `json:"topic,omitempty"`
	Language    string        `json:"language,omitempty"`
	Items       []newsItem    `json:"items"`
	Failures    []newsFailure `json:"failures,omitempty"`
	Summary     string        `json:"summary"`
}

type newsSourceRequest struct {
	Key      string
	Name     string
	URL      string
	Kind     string
	Region   string
	Topic    string
	Language string
	Locality string
	Section  string
}

type newsSourcePreset struct {
	Key      string
	Name     string
	URL      string
	Kind     string
	Region   string
	Language string
	Locality string
	Section  string
	Topic    string
}

type rssDocument struct {
	Channel struct {
		Title string    `xml:"title"`
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	PubDate     string   `xml:"pubDate"`
	Categories  []string `xml:"category"`
}

type atomDocument struct {
	Title   string      `xml:"title"`
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title      string         `xml:"title"`
	Summary    string         `xml:"summary"`
	Content    string         `xml:"content"`
	Updated    string         `xml:"updated"`
	Published  string         `xml:"published"`
	Categories []atomCategory `xml:"category"`
	Links      []atomLink     `xml:"link"`
}

type atomLink struct {
	Rel  string `xml:"rel,attr"`
	Href string `xml:"href,attr"`
}

type atomCategory struct {
	Term string `xml:"term,attr"`
}

type newsCandidate struct {
	Title       string
	URL         string
	Summary     string
	PublishedAt string
	Section     string
	Score       int
	Confidence  string
	FetchMethod string
}

var (
	reJSONLDScript = regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	reWordChar     = regexp.MustCompile(`[[:alnum:]]`)
)

var newsSourcePresets = []newsSourcePreset{
	{Key: "bbc_world", Name: "BBC News", URL: "https://feeds.bbci.co.uk/news/world/rss.xml", Kind: "feed", Region: "world", Language: "en", Section: "world"},
	{Key: "bbc_business", Name: "BBC News", URL: "https://feeds.bbci.co.uk/news/business/rss.xml", Kind: "feed", Region: "world", Topic: "business", Language: "en", Section: "business"},
	{Key: "bbc_technology", Name: "BBC News", URL: "https://feeds.bbci.co.uk/news/technology/rss.xml", Kind: "feed", Region: "world", Topic: "tech", Language: "en", Section: "technology"},
	{Key: "guardian_world", Name: "The Guardian", URL: "https://www.theguardian.com/world", Kind: "page", Region: "world", Language: "en", Section: "world"},
	{Key: "cbc_canada", Name: "CBC News", URL: "https://www.cbc.ca/webfeed/rss/rss-canada", Kind: "feed", Region: "canada", Language: "en", Section: "canada"},
	{Key: "cbc_business", Name: "CBC News", URL: "https://www.cbc.ca/webfeed/rss/rss-business", Kind: "feed", Region: "canada", Topic: "business", Language: "en", Section: "business"},
	{Key: "cbc_montreal", Name: "CBC Montreal", URL: "https://www.cbc.ca/webfeed/rss/rss-canada-montreal", Kind: "feed", Region: "montreal", Language: "en", Locality: "montreal", Section: "montreal"},
	{Key: "ctv_canada", Name: "CTV News", URL: "https://www.ctvnews.ca/canada", Kind: "page", Region: "canada", Language: "en", Section: "canada"},
	{Key: "ctv_montreal", Name: "CTV Montreal", URL: "https://montreal.ctvnews.ca/", Kind: "page", Region: "montreal", Language: "en", Locality: "montreal", Section: "montreal"},
	{Key: "global_canada", Name: "Global News", URL: "https://globalnews.ca/canada/", Kind: "page", Region: "canada", Language: "en", Section: "canada"},
	{Key: "global_montreal", Name: "Global Montreal", URL: "https://globalnews.ca/montreal/", Kind: "page", Region: "montreal", Language: "en", Locality: "montreal", Section: "montreal"},
	{Key: "lapresse_montreal", Name: "La Presse", URL: "https://www.lapresse.ca/actualites/grand-montreal/", Kind: "page", Region: "montreal", Language: "fr", Locality: "montreal", Section: "grand-montreal"},
	{Key: "lapresse_politique", Name: "La Presse", URL: "https://www.lapresse.ca/actualites/politique/", Kind: "page", Region: "quebec", Language: "fr", Section: "politique"},
	{Key: "radio_canada_montreal", Name: "Radio-Canada", URL: "https://ici.radio-canada.ca/regions/montreal/", Kind: "page", Region: "montreal", Language: "fr", Locality: "montreal", Section: "montreal"},
	{Key: "radio_canada_quebec", Name: "Radio-Canada", URL: "https://ici.radio-canada.ca/info", Kind: "page", Region: "quebec", Language: "fr", Section: "quebec"},
	{Key: "ledevoir", Name: "Le Devoir", URL: "https://www.ledevoir.com/", Kind: "page", Region: "quebec", Language: "fr", Section: "actualites"},
	{Key: "journaldemontreal", Name: "Journal de Montreal", URL: "https://www.journaldemontreal.com/", Kind: "page", Region: "montreal", Language: "fr", Locality: "montreal", Section: "montreal"},
	{Key: "tvanouvelles", Name: "TVA Nouvelles", URL: "https://www.tvanouvelles.ca/rss", Kind: "feed", Region: "quebec", Language: "fr", Topic: "general", Section: "general"},
	{Key: "lactualite", Name: "L'Actualité", URL: "https://lactualite.com/rss", Kind: "feed", Region: "quebec", Language: "fr", Topic: "general", Section: "general"},
	{Key: "ars_technica", Name: "Ars Technica", URL: "https://arstechnica.com/feed/", Kind: "feed", Region: "world", Topic: "tech", Language: "en", Section: "technology"},
	{Key: "ars_technica_science", Name: "Ars Technica", URL: "https://arstechnica.com/science/feed/", Kind: "feed", Region: "world", Topic: "science", Language: "en", Section: "science"},
	{Key: "ars_technica_culture", Name: "Ars Technica", URL: "https://arstechnica.com/culture/feed/", Kind: "feed", Region: "world", Topic: "culture", Language: "en", Section: "culture"},
}

func NewsFetch() Tool { return &newsFetchTool{} }

func (t *newsFetchTool) Name() string { return "news_fetch" }
func (t *newsFetchTool) Description() string {
	return "Fetch current news as normalized headline items. Uses feeds first when available, then source-aware headline extraction, and reports blocked or thin sources explicitly."
}
func (t *newsFetchTool) DangerLevel() DangerLevel { return Safe }
func (t *newsFetchTool) Effects() ToolEffects {
	return ToolEffects{NeedsNetwork: true, ExternalSideEffect: true}
}

func (t *newsFetchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"region": {"type": "string", "description": "News scope such as general, world, canada, quebec, or montreal."},
			"topic": {"type": "string", "description": "Optional topic hint such as general, politics, business, markets, or tech."},
			"language": {"type": "string", "description": "Preferred language: en, fr, or any.", "default": "any"},
			"sources": {"type": "array", "items": {"type": "string"}, "description": "Optional explicit source names or URLs. If omitted, sensible defaults are chosen for the region/topic."},
			"max_items": {"type": "integer", "description": "Maximum number of headlines to return.", "default": 8},
			"fresh_hours": {"type": "integer", "description": "If positive, prefer items published within this many hours when timestamps are available.", "default": 24},
			"fresh": {"type": "boolean", "description": "Force a fresh fetch, bypassing intermediate caches.", "default": false}
		}
	}`)
}

func (t *newsFetchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"generated_at": {"type": "string"},
			"region": {"type": "string"},
			"topic": {"type": "string"},
			"language": {"type": "string"},
			"items": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"headline": {"type": "string"},
						"source": {"type": "string"},
						"url": {"type": "string"},
						"published_at": {"type": "string"},
						"summary": {"type": "string"},
						"section": {"type": "string"},
						"region": {"type": "string"},
						"language": {"type": "string"},
						"locality": {"type": "string"},
						"confidence": {"type": "string"},
						"fetch_method": {"type": "string"}
					}
				}
			},
			"failures": {
				"type": "array",
				"items": {
					"type": "object",
					"properties": {
						"source": {"type": "string"},
						"url": {"type": "string"},
						"error": {"type": "string"},
						"status": {"type": "integer"}
					}
				}
			},
			"summary": {"type": "string"}
		}
	}`)
}

func (t *newsFetchTool) Exec(ctx context.Context, call ToolCallContext, args json.RawMessage) (ToolResult, error) {
	start := time.Now()

	var a newsFetchArgs
	a.Region = "general"
	a.Topic = "general"
	a.Language = "any"
	a.MaxItems = 8
	a.FreshHours = 24
	if err := json.Unmarshal(args, &a); err != nil {
		return failResult(start, "invalid args: "+err.Error()), nil
	}
	a.Region = normalizeNewsToken(a.Region, "general")
	a.Topic = normalizeNewsToken(a.Topic, "general")
	a.Language = normalizeNewsLanguage(a.Language)
	if a.MaxItems <= 0 || a.MaxItems > 20 {
		a.MaxItems = 8
	}
	if a.FreshHours < 0 || a.FreshHours > 24*14 {
		a.FreshHours = 24
	}

	requests := resolveNewsSourceRequests(a)
	if len(requests) == 0 {
		return failResult(start, "news_fetch: no matching sources for requested region/topic/language"), nil
	}

	perSourceLimit := 3
	if len(requests) == 1 || a.MaxItems < perSourceLimit {
		perSourceLimit = a.MaxItems
	}
	if perSourceLimit <= 0 {
		perSourceLimit = 1
	}

	fresh := a.Fresh

	items := make([]newsItem, 0, a.MaxItems)
	failures := make([]newsFailure, 0)
	seen := map[string]struct{}{}
	sourceOrder := map[string]int{}

	for idx, req := range requests {
		sourceOrder[req.Key] = idx
		sourceOrder[normalizeNewsToken(req.Name, req.Key)] = idx
		if len(items) >= a.MaxItems {
			break
		}
		sourceCall := call
		if sourceCall.TimeoutMS <= 0 || sourceCall.TimeoutMS > 15000 {
			sourceCall.TimeoutMS = 15000
		}

		got, fail := fetchNewsSource(ctx, sourceCall, req, a.FreshHours, fresh)
		if fail.Error != "" {
			failures = append(failures, fail)
		}

		addedForSource := 0
		for _, item := range got {
			if len(items) >= a.MaxItems || addedForSource >= perSourceLimit {
				break
			}
			key := normalizeNewsHeadline(item.Headline)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, item)
			addedForSource++
		}
	}

	if filtered := filterItemsByTopic(items, a.Topic); len(filtered) > 0 {
		items = filtered
	}

	sort.SliceStable(items, func(i, j int) bool {
		ti, oki := parseNewsTime(items[i].PublishedAt)
		tj, okj := parseNewsTime(items[j].PublishedAt)
		switch {
		case oki && okj:
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
		case oki:
			return true
		case okj:
			return false
		}
		return newsSourceOrder(items[i], sourceOrder, len(requests)) < newsSourceOrder(items[j], sourceOrder, len(requests))
	})
	if len(items) > a.MaxItems {
		items = items[:a.MaxItems]
	}

	out := newsFetchOutput{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Region:      a.Region,
		Topic:       a.Topic,
		Language:    a.Language,
		Items:       items,
		Failures:    failures,
		Summary:     buildNewsSummary(len(items), len(failures), requests),
	}

	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return failResult(start, "marshal output: "+err.Error()), nil
	}

	result := ToolResult{
		OK:         len(items) > 0,
		Output:     string(body),
		Stdout:     string(body),
		DurationMS: time.Since(start).Milliseconds(),
	}
	return sanitizeToolResult(call, result), nil
}

func fetchNewsSource(ctx context.Context, call ToolCallContext, req newsSourceRequest, freshHours int, fresh bool) ([]newsItem, newsFailure) {
	start := time.Now()
	resp, fail, err := fetchHTTP(ctx, call, start, req.URL, 256*1024, fresh)
	if err != nil {
		return nil, newsFailure{Source: req.Name, URL: req.URL, Error: err.Error()}
	}
	if fail != nil {
		return nil, newsFailure{Source: req.Name, URL: req.URL, Error: fail.Output}
	}

	if resp.status < 200 || resp.status >= 400 {
		return nil, newsFailure{
			Source: req.Name,
			URL:    req.URL,
			Status: resp.status,
			Error:  classifyNewsHTTPFailure(resp),
		}
	}

	bodyText := string(resp.body)
	var (
		items []newsItem
		e     error
	)
	switch {
	case req.Kind == "feed" || looksLikeNewsFeed(resp.contentType, bodyText):
		items, e = parseNewsFeed(req, resp.body)
		if e != nil {
			return nil, newsFailure{Source: req.Name, URL: req.URL, Status: resp.status, Error: "feed parse failed: " + e.Error()}
		}
	case strings.Contains(resp.contentType, "html") || looksLikeHTML(bodyText):
		items, e = extractNewsItemsFromHTML(req, bodyText)
		if e != nil {
			return nil, newsFailure{Source: req.Name, URL: req.URL, Status: resp.status, Error: "html parse failed: " + e.Error()}
		}
	default:
		return nil, newsFailure{Source: req.Name, URL: req.URL, Status: resp.status, Error: "unsupported content type: " + displayHTTPContentType(resp.contentType, resp.body)}
	}

	if freshHours > 0 {
		items = filterFreshNewsItems(items, freshHours)
	}
	if len(items) == 0 {
		return nil, newsFailure{
			Source: req.Name,
			URL:    req.URL,
			Status: resp.status,
			Error:  "reachable but no usable headlines extracted",
		}
	}
	return items, newsFailure{}
}

func resolveNewsSourceRequests(a newsFetchArgs) []newsSourceRequest {
	if len(a.Sources) > 0 {
		out := make([]newsSourceRequest, 0, len(a.Sources))
		seen := map[string]struct{}{}
		for _, raw := range a.Sources {
			reqs := resolveExplicitNewsSource(raw, a)
			for _, req := range reqs {
				key := req.Key + "|" + req.URL
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				out = append(out, req)
			}
		}
		return out
	}

	var wanted []string
	switch a.Region {
	case "montreal":
		if a.Language != "fr" {
			wanted = append(wanted, "ctv_montreal", "global_montreal", "cbc_montreal")
		}
		if a.Language != "en" {
			wanted = append(wanted, "lapresse_montreal", "radio_canada_montreal", "journaldemontreal")
		}
	case "quebec":
		if a.Language != "en" {
			wanted = append(wanted, "lapresse_politique", "radio_canada_quebec", "ledevoir")
			wanted = append(wanted, "tvanouvelles", "lactualite")
		}
		if a.Language != "fr" {
			wanted = append(wanted, "cbc_canada", "ctv_canada")
		}
	case "canada":
		if a.Topic == "business" || a.Topic == "markets" {
			wanted = append(wanted, "cbc_business")
		} else {
			wanted = append(wanted, "cbc_canada")
		}
		wanted = append(wanted, "ctv_canada", "global_canada")
		if a.Language != "en" {
			wanted = append(wanted, "lapresse_politique")
		}
	case "world":
		switch a.Topic {
		case "business", "markets":
			wanted = append(wanted, "bbc_business")
		case "tech", "technology":
			wanted = append(wanted, "bbc_technology", "ars_technica")
		case "science":
			wanted = append(wanted, "ars_technica_science")
		case "culture":
			wanted = append(wanted, "ars_technica_culture")
		default:
			wanted = append(wanted, "bbc_world")
		}
		if a.Topic == "general" || a.Topic == "" {
			wanted = append(wanted, "guardian_world")
		}
	default:
		// "general" region or unknown: apply topic filtering like "world" does
		switch a.Topic {
		case "business", "markets":
			wanted = append(wanted, "bbc_business")
		case "tech", "technology":
			wanted = append(wanted, "bbc_technology", "ars_technica")
		case "science":
			wanted = append(wanted, "ars_technica_science")
		case "culture":
			wanted = append(wanted, "ars_technica_culture")
		default:
			// No specific topic: include general news sources
			wanted = append(wanted, "bbc_world", "cbc_canada", "ctv_canada")
			if a.Language != "en" {
				wanted = append(wanted, "lapresse_politique")
			}
		}
	}

	out := make([]newsSourceRequest, 0, len(wanted))
	seen := map[string]struct{}{}
	for _, key := range wanted {
		req, ok := presetToNewsRequest(key)
		if !ok {
			continue
		}
		if a.Language != "any" && req.Language != "" && req.Language != a.Language {
			continue
		}
		dedupeKey := req.Key + "|" + req.URL
		if _, ok := seen[dedupeKey]; ok {
			continue
		}
		seen[dedupeKey] = struct{}{}
		out = append(out, req)
	}
	return out
}

func resolveExplicitNewsSource(raw string, a newsFetchArgs) []newsSourceRequest {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if looksLikeURL(raw) {
		lang := a.Language
		if lang == "any" {
			lang = ""
		}
		return []newsSourceRequest{{
			Key:      strings.ToLower(sourceNameFromURL(raw)),
			Name:     sourceNameFromURL(raw),
			URL:      raw,
			Kind:     inferNewsURLKind(raw),
			Region:   a.Region,
			Topic:    a.Topic,
			Language: lang,
			Locality: a.Region,
			Section:  a.Region,
		}}
	}

	key := normalizeNewsToken(raw, "")
	switch key {
	case "bbc":
		key = "bbc_world"
	case "cbc":
		if a.Region == "montreal" {
			key = "cbc_montreal"
		} else {
			key = "cbc_canada"
		}
	case "ctv":
		if a.Region == "montreal" {
			key = "ctv_montreal"
		} else {
			key = "ctv_canada"
		}
	case "global":
		if a.Region == "montreal" {
			key = "global_montreal"
		} else {
			key = "global_canada"
		}
	case "lapresse", "la_presse":
		if a.Region == "montreal" {
			key = "lapresse_montreal"
		} else {
			key = "lapresse_politique"
		}
	case "radio_canada", "radio-canada":
		if a.Region == "montreal" {
			key = "radio_canada_montreal"
		} else {
			key = "radio_canada_quebec"
		}
	case "jdm":
		key = "journaldemontreal"
	}

	req, ok := presetToNewsRequest(key)
	if !ok {
		return []newsSourceRequest{{
			Key:  key,
			Name: raw,
			URL:  raw,
			Kind: inferNewsURLKind(raw),
		}}
	}
	return []newsSourceRequest{req}
}

func presetToNewsRequest(key string) (newsSourceRequest, bool) {
	for _, preset := range newsSourcePresets {
		if preset.Key != key {
			continue
		}
		return newsSourceRequest{
			Key:      preset.Key,
			Name:     preset.Name,
			URL:      preset.URL,
			Kind:     preset.Kind,
			Region:   preset.Region,
			Topic:    preset.Topic,
			Language: preset.Language,
			Locality: preset.Locality,
			Section:  preset.Section,
		}, true
	}
	return newsSourceRequest{}, false
}

func parseNewsFeed(req newsSourceRequest, body []byte) ([]newsItem, error) {
	var rss rssDocument
	if err := xml.Unmarshal(sanitizeXMLBody(body), &rss); err == nil && len(rss.Channel.Items) > 0 {
		source := req.Name
		if strings.TrimSpace(rss.Channel.Title) != "" {
			source = strings.TrimSpace(rss.Channel.Title)
		}
		items := make([]newsItem, 0, len(rss.Channel.Items))
		for _, item := range rss.Channel.Items {
			headline := collapseLineNoise(cleanHTMLFragment(item.Title))
			if !looksLikeNewsHeadline(headline, "") {
				continue
			}
			link := strings.TrimSpace(item.Link)
			summary := trimSummary(htmlToText(item.Description))
			publishedAt := normalizeNewsTime(item.PubDate)
			section := req.Section
			if section == "" && len(item.Categories) > 0 {
				section = collapseLineNoise(item.Categories[0])
			}
			items = append(items, newsItem{
				Headline:    headline,
				Source:      source,
				URL:         link,
				PublishedAt: publishedAt,
				Summary:     summary,
				Section:     section,
				Region:      req.Region,
				Language:    req.Language,
				Locality:    req.Locality,
				Confidence:  "high",
				FetchMethod: "rss",
			})
		}
		return items, nil
	}

	var atom atomDocument
	if err := xml.Unmarshal(sanitizeXMLBody(body), &atom); err != nil {
		return nil, err
	}
	source := req.Name
	if strings.TrimSpace(atom.Title) != "" {
		source = strings.TrimSpace(atom.Title)
	}
	items := make([]newsItem, 0, len(atom.Entries))
	for _, entry := range atom.Entries {
		headline := collapseLineNoise(cleanHTMLFragment(entry.Title))
		if !looksLikeNewsHeadline(headline, "") {
			continue
		}
		link := ""
		for _, l := range entry.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = strings.TrimSpace(l.Href)
				break
			}
		}
		summary := trimSummary(htmlToText(entry.Summary))
		if summary == "" {
			summary = trimSummary(htmlToText(entry.Content))
		}
		publishedAt := normalizeNewsTime(entry.Published)
		if publishedAt == "" {
			publishedAt = normalizeNewsTime(entry.Updated)
		}
		section := req.Section
		if section == "" && len(entry.Categories) > 0 {
			section = collapseLineNoise(entry.Categories[0].Term)
		}
		items = append(items, newsItem{
			Headline:    headline,
			Source:      source,
			URL:         link,
			PublishedAt: publishedAt,
			Summary:     summary,
			Section:     section,
			Region:      req.Region,
			Language:    req.Language,
			Locality:    req.Locality,
			Confidence:  "high",
			FetchMethod: "atom",
		})
	}
	return items, nil
}

func extractNewsItemsFromHTML(req newsSourceRequest, body string) ([]newsItem, error) {
	base, _ := url.Parse(req.URL)
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	candidates := make([]newsCandidate, 0, 12)
	candidates = append(candidates, extractJSONLDNewsCandidates(base, req, body)...)
	candidates = append(candidates, extractDOMNewsCandidates(base, req, doc)...)
	candidates = dedupeNewsCandidates(candidates)

	items := make([]newsItem, 0, len(candidates))
	for _, cand := range candidates {
		items = append(items, newsItem{
			Headline:    cand.Title,
			Source:      req.Name,
			URL:         cand.URL,
			PublishedAt: normalizeNewsTime(cand.PublishedAt),
			Summary:     trimSummary(cand.Summary),
			Section:     firstNonEmpty(cand.Section, req.Section),
			Region:      req.Region,
			Language:    req.Language,
			Locality:    req.Locality,
			Confidence:  cand.Confidence,
			FetchMethod: cand.FetchMethod,
		})
	}
	return items, nil
}

func extractJSONLDNewsCandidates(base *url.URL, req newsSourceRequest, body string) []newsCandidate {
	matches := reJSONLDScript.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]newsCandidate, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		var payload any
		if err := json.Unmarshal([]byte(match[1]), &payload); err != nil {
			continue
		}
		out = append(out, walkJSONLDNews(payload, base, req)...)
	}
	return out
}

func walkJSONLDNews(payload any, base *url.URL, req newsSourceRequest) []newsCandidate {
	switch v := payload.(type) {
	case []any:
		out := make([]newsCandidate, 0)
		for _, item := range v {
			out = append(out, walkJSONLDNews(item, base, req)...)
		}
		return out
	case map[string]any:
		out := make([]newsCandidate, 0)
		if item := jsonLDMapToCandidate(v, base, req); item.Title != "" {
			out = append(out, item)
		}
		for _, key := range []string{"@graph", "itemListElement", "mainEntity", "mainEntityOfPage"} {
			if next, ok := v[key]; ok {
				out = append(out, walkJSONLDNews(next, base, req)...)
			}
		}
		return out
	default:
		return nil
	}
}

func jsonLDMapToCandidate(m map[string]any, base *url.URL, req newsSourceRequest) newsCandidate {
	headline := strings.TrimSpace(stringValue(m["headline"]))
	if headline == "" {
		headline = strings.TrimSpace(stringValue(m["name"]))
	}
	headline = collapseLineNoise(cleanHTMLFragment(headline))
	if !looksLikeNewsHeadline(headline, "") {
		return newsCandidate{}
	}

	link := stringValue(m["url"])
	if link == "" {
		link = stringValueFromMap(m["mainEntityOfPage"], "url")
	}
	if link != "" {
		link = resolveNewsURL(base, link)
	}

	publishedAt := firstNonEmpty(stringValue(m["datePublished"]), stringValue(m["dateCreated"]))
	summary := firstNonEmpty(stringValue(m["description"]), stringValue(m["abstract"]))
	section := firstNonEmpty(stringValue(m["articleSection"]), req.Section)
	return newsCandidate{
		Title:       headline,
		URL:         link,
		Summary:     summary,
		PublishedAt: publishedAt,
		Section:     section,
		Score:       100,
		Confidence:  "high",
		FetchMethod: "html_jsonld",
	}
}

func extractDOMNewsCandidates(base *url.URL, req newsSourceRequest, doc *xhtml.Node) []newsCandidate {
	pageTitle := extractHTMLTitle(doc)
	out := make([]newsCandidate, 0, 12)
	seen := map[string]struct{}{}

	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil {
			return
		}
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "a":
				text := collapseLineNoise(nodeText(n))
				href := attrValue(n, "href")
				score := scoreNewsAnchor(n, text, href, pageTitle)
				if score >= 35 && looksLikeNewsHeadline(text, pageTitle) {
					key := normalizeNewsHeadline(text)
					if _, ok := seen[key]; !ok {
						seen[key] = struct{}{}
						out = append(out, newsCandidate{
							Title:       text,
							URL:         resolveNewsURL(base, href),
							Summary:     extractAnchorSummary(n),
							PublishedAt: extractNearbyTime(n),
							Section:     req.Section,
							Score:       score,
							Confidence:  confidenceForScore(score),
							FetchMethod: "html_headlines",
						})
					}
				}
			case "h1", "h2", "h3", "h4":
				text := collapseLineNoise(nodeText(n))
				if looksLikeNewsHeadline(text, pageTitle) && !strings.EqualFold(text, pageTitle) {
					key := normalizeNewsHeadline(text)
					if _, ok := seen[key]; !ok {
						seen[key] = struct{}{}
						out = append(out, newsCandidate{
							Title:       text,
							URL:         resolveNewsURL(base, firstDescendantHref(n)),
							Summary:     extractHeadingSummary(n),
							PublishedAt: extractNearbyTime(n),
							Section:     req.Section,
							Score:       55,
							Confidence:  "medium",
							FetchMethod: "html_headlines",
						})
					}
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func dedupeNewsCandidates(items []newsCandidate) []newsCandidate {
	best := make(map[string]newsCandidate, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		key := normalizeNewsHeadline(item.Title)
		if key == "" {
			continue
		}
		current, ok := best[key]
		if !ok {
			best[key] = item
			order = append(order, key)
			continue
		}
		if item.Score > current.Score || (item.Score == current.Score && len(item.Summary) > len(current.Summary)) {
			best[key] = item
		}
	}
	out := make([]newsCandidate, 0, len(order))
	for _, key := range order {
		out = append(out, best[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func filterFreshNewsItems(items []newsItem, freshHours int) []newsItem {
	if freshHours <= 0 {
		return items
	}
	cutoff := time.Now().UTC().Add(-time.Duration(freshHours) * time.Hour)
	filtered := make([]newsItem, 0, len(items))
	unknownTime := make([]newsItem, 0, len(items))
	for _, item := range items {
		ts, ok := parseNewsTime(item.PublishedAt)
		if !ok {
			unknownTime = append(unknownTime, item)
			continue
		}
		if !ts.Before(cutoff) {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) > 0 {
		return filtered
	}
	return oldestWhenStale(unknownTime, items)
}

func filterItemsByTopic(items []newsItem, topic string) []newsItem {
	topic = normalizeNewsToken(topic, "general")
	if topic == "" || topic == "general" {
		return items
	}
	filtered := make([]newsItem, 0, len(items))
	for _, item := range items {
		if newsItemMatchesTopic(item, topic) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func newsItemMatchesTopic(item newsItem, topic string) bool {
	text := strings.ToLower(item.Headline + " " + item.Section + " " + item.Summary)
	keywords := map[string][]string{
		"politics":   {"politic", "election", "minister", "parliament", "government", "budget", "law", "policy"},
		"business":   {"business", "economy", "market", "budget", "trade", "company", "jobs", "inflation"},
		"markets":    {"market", "stocks", "bond", "rates", "earnings", "trading", "economy", "jobs"},
		"tech":       {"tech", "technology", "ai", "software", "chip", "app", "internet", "cyber"},
		"technology": {"tech", "technology", "ai", "software", "chip", "app", "internet", "cyber"},
		"science":    {"science", "research", "study", "space", "astronomy", "nasa", "physics", "biology", "climate", "medicine"},
		"culture":    {"culture", "media", "film", "movie", "tv", "television", "music", "game", "gaming", "book", "art", "entertainment"},
	}
	for _, keyword := range keywords[topic] {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func classifyNewsHTTPFailure(resp fetchedHTTPResponse) string {
	if resp.status == 401 || resp.status == 403 {
		text := strings.ToLower(describeHTTPBody(resp.contentType, resp.body))
		if strings.Contains(text, "enable js") || strings.Contains(text, "disable any ad blocker") {
			return fmt.Sprintf("http %d: page blocked or requires javascript", resp.status)
		}
		return fmt.Sprintf("http %d: access blocked", resp.status)
	}
	return fmt.Sprintf("http %d", resp.status)
}

func buildNewsSummary(itemCount, failureCount int, reqs []newsSourceRequest) string {
	sourceCount := len(reqs)
	switch {
	case itemCount > 0 && failureCount > 0:
		return fmt.Sprintf("%d headline items from %d configured sources; %d sources failed or returned no usable headlines", itemCount, sourceCount, failureCount)
	case itemCount > 0:
		return fmt.Sprintf("%d headline items from %d configured sources", itemCount, sourceCount)
	case failureCount > 0:
		return fmt.Sprintf("no headline items extracted; %d configured sources failed or returned no usable headlines", failureCount)
	default:
		return "no headline items extracted"
	}
}

func normalizeNewsToken(s, fallback string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	if s == "" {
		return fallback
	}
	return s
}

func normalizeNewsLanguage(s string) string {
	s = normalizeNewsToken(s, "any")
	switch s {
	case "fr", "en", "any":
		return s
	default:
		return "any"
	}
}

func newsSourceKey(item newsItem) string {
	return normalizeNewsToken(item.Source, "")
}

func newsSourceOrder(item newsItem, sourceOrder map[string]int, fallback int) int {
	if idx, ok := sourceOrder[newsSourceKey(item)]; ok {
		return idx
	}
	return fallback
}

func normalizeNewsHeadline(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("’", "'", "“", "\"", "”", "\"", "-", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func looksLikeURL(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), "http://") || strings.HasPrefix(strings.TrimSpace(s), "https://")
}

func inferNewsURLKind(s string) string {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "rss") || strings.Contains(lower, "feed") || strings.HasSuffix(lower, ".xml") {
		return "feed"
	}
	return "page"
}

func looksLikeNewsFeed(contentType, body string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "xml") || strings.Contains(ct, "rss") || strings.Contains(ct, "atom") {
		return true
	}
	body = strings.ToLower(strings.TrimSpace(body))
	return strings.HasPrefix(body, "<?xml") || strings.Contains(body, "<rss") || strings.Contains(body, "<feed")
}

func sourceNameFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "Custom Source"
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	parts := strings.Split(host, ".")
	if len(parts) == 0 {
		return host
	}
	return strings.ToUpper(parts[0][:1]) + parts[0][1:]
}

func parseNewsTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"Mon, 02 Jan 2006 15:04:05 MST",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, value); err == nil {
			return ts.UTC(), true
		}
	}
	return time.Time{}, false
}

func normalizeNewsTime(value string) string {
	if ts, ok := parseNewsTime(value); ok {
		return ts.Format(time.RFC3339)
	}
	return ""
}

func trimSummary(s string) string {
	s = collapseLineNoise(cleanHTMLFragment(s))
	if len(s) > 280 {
		s = s[:280]
		s = strings.TrimSpace(s)
	}
	return s
}

func resolveNewsURL(base *url.URL, href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return href
	}
	if base == nil {
		return ref.String()
	}
	return base.ResolveReference(ref).String()
}

func stringValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if s := stringValue(x["url"]); s != "" {
			return s
		}
		if s := stringValue(x["@id"]); s != "" {
			return s
		}
	case []any:
		for _, item := range x {
			if s := stringValue(item); s != "" {
				return s
			}
		}
	}
	return ""
}

func stringValueFromMap(v any, key string) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(m[key])
}

func extractHTMLTitle(doc *xhtml.Node) string {
	var title string
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n == nil || title != "" {
			return
		}
		if n.Type == xhtml.ElementNode && n.Data == "title" {
			title = collapseLineNoise(nodeText(n))
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return title
}

func nodeText(n *xhtml.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	if n.Type == xhtml.ElementNode && (n.Data == "script" || n.Data == "style") {
		return ""
	}
	var b strings.Builder
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		text := nodeText(child)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return b.String()
}

func attrValue(n *xhtml.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func firstDescendantHref(n *xhtml.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == xhtml.ElementNode && n.Data == "a" {
		return attrValue(n, "href")
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if href := firstDescendantHref(child); href != "" {
			return href
		}
	}
	return ""
}

func scoreNewsAnchor(n *xhtml.Node, text, href, pageTitle string) int {
	if !looksLikeNewsHeadline(text, pageTitle) {
		return 0
	}
	score := 10
	if href == "" || strings.HasPrefix(href, "#") || strings.HasPrefix(strings.ToLower(href), "javascript:") {
		return 0
	}
	lowerHref := strings.ToLower(href)
	for _, bad := range []string{"weather", "traffic", "video", "watch", "newsletter", "subscribe", "account", "privacy", "cookie", "login"} {
		if strings.Contains(lowerHref, bad) {
			score -= 30
		}
	}
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type != xhtml.ElementNode {
			continue
		}
		switch cur.Data {
		case "h1", "h2", "h3", "h4":
			score += 45
		case "article":
			score += 20
		case "li", "section":
			score += 10
		}
		classAttr := strings.ToLower(attrValue(cur, "class") + " " + attrValue(cur, "id"))
		for _, keyword := range []string{"headline", "title", "story", "article", "card", "teaser", "post"} {
			if strings.Contains(classAttr, keyword) {
				score += 15
			}
		}
	}
	return score
}

func extractAnchorSummary(n *xhtml.Node) string {
	container := nearestContainerNode(n)
	if container == nil {
		return ""
	}
	for child := container.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xhtml.ElementNode && child.Data == "p" {
			text := collapseLineNoise(nodeText(child))
			if len(text) >= 30 {
				return text
			}
		}
	}
	return ""
}

func extractHeadingSummary(n *xhtml.Node) string {
	return extractAnchorSummary(n)
}

func extractNearbyTime(n *xhtml.Node) string {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type == xhtml.ElementNode && cur.Data == "time" {
			if value := attrValue(cur, "datetime"); value != "" {
				return value
			}
			if value := collapseLineNoise(nodeText(cur)); value != "" {
				return value
			}
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.ElementNode && child.Data == "time" {
				if value := attrValue(child, "datetime"); value != "" {
					return value
				}
				if value := collapseLineNoise(nodeText(child)); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func nearestContainerNode(n *xhtml.Node) *xhtml.Node {
	for cur := n; cur != nil; cur = cur.Parent {
		if cur.Type != xhtml.ElementNode {
			continue
		}
		switch cur.Data {
		case "article", "li", "div", "section":
			return cur
		}
	}
	return nil
}

func confidenceForScore(score int) string {
	switch {
	case score >= 80:
		return "high"
	case score >= 50:
		return "medium"
	default:
		return "low"
	}
}

func looksLikeNewsHeadline(text, pageTitle string) bool {
	text = collapseLineNoise(text)
	if text == "" || len(text) < 16 || len(text) > 180 {
		return false
	}
	if !reWordChar.MatchString(text) {
		return false
	}
	if pageTitle != "" && strings.EqualFold(text, pageTitle) {
		return false
	}
	lower := strings.ToLower(text)
	if strings.Count(text, " ") < 2 {
		return false
	}
	for _, blocked := range []string{
		"breaking news", "latest news", "top stories", "watch live", "sign in", "newsletter",
		"privacy policy", "terms of use", "montreal", "canada", "quebec", "world", "news",
		"associated press news", "ap news", "bbc news", "global news", "la presse", "le devoir", "radio-canada",
	} {
		if lower == blocked {
			return false
		}
	}
	for _, blocked := range []string{
		"cookie", "privacy", "newsletter", "subscribe", "watch live", "sign in", "traffic", "weather",
		"video", "advertisement", "sponsored", "live updates",
	} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// sanitizeXMLBody fixes common XML well-formedness issues found in real-world
// RSS feeds. In particular, many feeds (e.g. TVA Nouvelles) include bare "&"
// characters in URL query parameters inside attributes (e.g. &x=0&y=178) which
// are not valid XML entity references and cause Go's strict XML parser to fail.
func sanitizeXMLBody(body []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(len(body) + len(body)/20)
	i := 0
	for i < len(body) {
		if body[i] == '&' {
			if isValidXMLEntity(body[i:]) {
				buf.WriteByte(body[i])
			} else {
				buf.WriteString("&amp;")
			}
		} else {
			buf.WriteByte(body[i])
		}
		i++
	}
	return buf.Bytes()
}

// isValidXMLEntity checks whether the bytes starting at '&' form a valid XML
// entity reference such as &amp; &lt; &gt; &apos; &quot; &#123; &#xAB;
func isValidXMLEntity(s []byte) bool {
	if len(s) < 2 || s[0] != '&' {
		return false
	}
	// Named entities
	for _, name := range []string{"amp;", "lt;", "gt;", "apos;", "quot;"} {
		if len(s) > len(name) && string(s[1:1+len(name)]) == name {
			return true
		}
	}
	// Numeric character references: &#NNN; or &#xHHH;
	if len(s) > 2 && s[1] == '#' {
		j := 2
		if s[2] == 'x' || s[2] == 'X' {
			j = 3
		}
		for j < len(s) {
			if s[j] == ';' {
				return true
			}
			if !isXMLNameChar(s[j]) {
				return false
			}
			j++
		}
		return false
	}
	// General entity: &name;
	for j := 1; j < len(s); j++ {
		if s[j] == ';' {
			return true
		}
		if !isXMLNameChar(s[j]) {
			return false
		}
	}
	return false
}

func isXMLNameChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_' || b == '-' || b == '.'
}

// oldestWhenStale is the fallback for filterFreshNewsItems. If we have items
// with unknown timestamps, those are returned (prefer ambiguity over dropping).
// If all items had timestamps but none passed the freshness cutoff, we return
// the original items so that infrequently-updated feeds (e.g. L'Actualité)
// still produce results rather than appearing as failures.
func oldestWhenStale(unknownTime, allItems []newsItem) []newsItem {
	if len(unknownTime) > 0 {
		return unknownTime
	}
	return allItems
}
