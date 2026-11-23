package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/zood/video-manager/internal/platform"
)

func (s *Source) attachEmbeds(ctx context.Context, caller platform.Caller, parent string, embeds []platform.Embed, rows []map[string]any) error {
	for _, embed := range embeds {
		if err := s.attachEmbed(ctx, caller, embed, rows); err != nil {
			return err
		}
	}
	return nil
}

func (s *Source) attachEmbed(ctx context.Context, caller platform.Caller, embed platform.Embed, rows []map[string]any) error {
	ids := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		id, _ := row[embed.Local].(string)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	grouped := map[string][]map[string]any{}
	if len(ids) > 0 {
		loaded, err := s.loadEmbed(ctx, caller, embed, ids)
		if err != nil {
			return err
		}
		if err := s.attachEmbeds(ctx, caller, embed.Table, embed.Nested, loaded); err != nil {
			return err
		}
		for _, item := range loaded {
			key, _ := item[embed.Remote].(string)
			grouped[key] = append(grouped[key], item)
		}
	}
	for _, row := range rows {
		id, _ := row[embed.Local].(string)
		matches := grouped[id]
		if embed.Many {
			if matches == nil {
				matches = []map[string]any{}
			}
			row[embed.Key] = matches
			continue
		}
		if len(matches) == 0 {
			row[embed.Key] = nil
			continue
		}
		row[embed.Key] = matches[0]
	}
	return nil
}

func (s *Source) loadEmbed(ctx context.Context, caller platform.Caller, embed platform.Embed, ids []string) ([]map[string]any, error) {
	columns := append([]string{}, embed.Columns...)
	if !hasString(columns, embed.Remote) {
		columns = append([]string{embed.Remote}, columns...)
	}
	args := make([]any, len(ids))
	holders := make([]string, len(ids))
	for i, id := range ids {
		args[i] = id
		holders[i] = "$" + strconv.Itoa(i+1)
	}
	text := "SELECT " + strings.Join(columns, ", ") + " FROM " + embed.Table + " WHERE " + embed.Remote + " IN (" + strings.Join(holders, ", ") + ")"
	return s.queryMaps(ctx, caller, text, args)
}

func hasString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
