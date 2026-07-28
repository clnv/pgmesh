package sqlcplugin

import (
	"testing"

	"github.com/sqlc-dev/plugin-sdk-go/plugin"
	"github.com/stretchr/testify/assert"
)

func TestQueryRelationIdentifiers(t *testing.T) {
	t.Parallel()

	got := queryRelationIdentifiers(`
		SELECT 'FROM ignored_string', $$ JOIN ignored_dollar_quote $$
		FROM ONLY public."message" AS m
		JOIN "audit"."MessageInbox" AS mi ON mi.message_id = m.id
		-- JOIN ignored_line_comment
		/* FROM ignored_block_comment */
	`)

	assert.Equal(t, []*plugin.Identifier{
		{Schema: "public", Name: "message"},
		{Schema: "audit", Name: "MessageInbox"},
	}, got)
}
