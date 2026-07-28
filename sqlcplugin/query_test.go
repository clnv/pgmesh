package sqlcplugin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseShardAnnotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		comment string
		want    *routeAnnotation
		wantErr string
	}{
		{
			name:    "one operand",
			comment: "shard: tenant(tenant_id)",
			want:    &routeAnnotation{name: "tenant", operands: []string{"tenant_id"}},
		},
		{
			name:    "multiple operands and spaces",
			comment: "shard: p2p( user_id, peer_id )",
			want:    &routeAnnotation{name: "p2p", operands: []string{"user_id", "peer_id"}},
		},
		{name: "no operands", comment: "shard: global()", want: &routeAnnotation{name: "global"}},
		{name: "missing parentheses", comment: "shard: tenant", wantErr: "malformed shard annotation"},
		{name: "invalid route", comment: "shard: tenant-route(id)", wantErr: "invalid shard route name"},
		{name: "invalid operand", comment: "shard: tenant(tenant-id)", wantErr: "invalid shard operand"},
		{name: "duplicate operand", comment: "shard: tenant(id, id)", wantErr: "repeats shard operand"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseShardAnnotation("GetUser", test.comment)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestParseStoreAnnotation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		comment string
		want    string
		wantErr string
	}{
		{name: "exported identifier", comment: "store: Accounts", want: "Accounts"},
		{name: "acronym", comment: "store: IAM", want: "IAM"},
		{name: "empty", comment: "store:", wantErr: "expected an exported Go identifier"},
		{name: "unexported", comment: "store: accounts", wantErr: "expected an exported Go identifier"},
		{name: "punctuation", comment: "store: User-Accounts", wantErr: "expected an exported Go identifier"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseStoreAnnotation("GetUser", test.comment)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestQueryReturnsData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    bool
	}{
		{command: ":one", want: true},
		{command: ":many", want: true},
		{command: ":exec", want: false},
		{command: ":execrows", want: false},
		{command: ":execresult", want: false},
		{command: ":copyfrom", want: false},
		{command: ":batchexec", want: false},
		{command: ":batchone", want: true},
		{command: ":batchmany", want: true},
	}

	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.want, queryReturnsData(test.command))
		})
	}
}
