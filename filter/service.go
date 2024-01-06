package filter

import (
	"regexp"
	"strings"

	"github.com/dewey/miniflux-sidekick/rules"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	miniflux "miniflux.app/client"
)

// Service is an interface for a filter service
type Service interface {
	RunFilterJob(simulation bool)
	Run()
}

type service struct {
	rulesRepository rules.Repository
	client          *miniflux.Client
	l               log.Logger
}

// NewService initializes a new filter service
func NewService(l log.Logger, c *miniflux.Client, rr rules.Repository) Service {
	return &service{
		rulesRepository: rr,
		client:          c,
		l:               l,
	}
}

func (s *service) Run() {
	s.RunFilterJob(false)
}

var filterEntryRegex = regexp.MustCompile(`(\w+?) (\S+?) (.+)`)

func (s *service) RunFilterJob(simulation bool) {
	level.Info(s.l).Log("filter", "start")
	f, err := s.client.Feeds()
	if err != nil {
		level.Error(s.l).Log("err", err)
		return
	}
	totalMatched := 0
	for _, feed := range f {
		feedLogged := false
		entries, err := s.client.FeedEntries(feed.ID, &miniflux.Filter{
			Status: miniflux.EntryStatusUnread,
		})
		if err != nil {
			level.Error(s.l).Log("err", err)
			continue
		}
		for _, entry := range entries.Entries {
			for _, rule := range s.rulesRepository.Rules() {
				skip := true
				if rule.URL == "*" {
					skip = false
				} else {
					matched, err := regexp.MatchString(rule.URL, feed.FeedURL)
					if err != nil {
						level.Error(s.l).Log("err", err)
					} else if matched {
						skip = false
					}
				}
				if skip {
					continue
				}
				if s.evaluateRule(entry, rule) {
					totalMatched += 1
					if !feedLogged {
						level.Info(s.l).Log("feed", feed.Title, "ID", feed.ID, "url", feed.FeedURL)
						feedLogged = true
					}
					level.Info(s.l).Log("filtering", entry.Title, "matches_rule", rule.FilterExpression)
					if !simulation {
						if err := s.client.UpdateEntries([]int64{entry.ID}, miniflux.EntryStatusRead); err != nil {
							level.Error(s.l).Log("msg", "error on updating the feed entries", "ids", entry.ID, "err", err)
							return
						}
					}
				}
			}
		}
	}
	level.Info(s.l).Log("filter", "end", "filtered", totalMatched)
}

func (s service) evaluateRule(entry *miniflux.Entry, rule rules.Rule) bool {
	var shouldKill bool
	tokens := filterEntryRegex.FindStringSubmatch(rule.FilterExpression)
	if tokens == nil || len(tokens) != 4 {
		level.Error(s.l).Log("err", "invalid filter expression", "expression", rule.FilterExpression)
		return false
	}
	// We set the string we want to compare against (https://newsboat.org/releases/2.15/docs/newsboat.html#_filter_language are supported in the killfile format)
	var entryTarget string
	switch tokens[1] {
	case "title":
		entryTarget = entry.Title
	case "description":
		entryTarget = entry.Content
	case "tags":
		switch tokens[2] {
		case "#", "!#":
			invertFilter := tokens[2][0] == '!'
			for _, tag := range entry.Tags {
				matched, err := regexp.MatchString(tokens[3], tag)
				if err != nil {
					level.Error(s.l).Log("err", err)
				}
				if matched && !invertFilter || !matched && invertFilter {
					return true
				}
			}
		default:
			level.Error(s.l).Log("err", "invalid filter expression: 'tags' only support # and #!", "expression", rule.FilterExpression)
		}
	}

	// We check what kind of comparator was given
	switch tokens[2] {
	case "=~", "!~":
		invertFilter := tokens[2][0] == '!'

		matched, err := regexp.MatchString(tokens[3], entryTarget)
		if err != nil {
			level.Error(s.l).Log("err", err)
		}

		if matched && !invertFilter || !matched && invertFilter {
			shouldKill = true
		}
	case "#", "!#":
		invertFilter := tokens[2][0] == '!'

		var containsTerm bool
		blacklistTokens := strings.Split(tokens[3], ",")
		for _, t := range blacklistTokens {
			if strings.Contains(entryTarget, t) {
				containsTerm = true
				break
			}
		}
		if containsTerm && !invertFilter || !containsTerm && invertFilter {
			shouldKill = true
		}
	}
	return shouldKill
}
