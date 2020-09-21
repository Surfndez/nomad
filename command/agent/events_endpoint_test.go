package agent

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventStream_QueryParse(t *testing.T) {
	cases := []struct {
		desc    string
		query   string
		want    map[string][]string
		wantErr bool
	}{
		{
			desc:  "all topics and keys specified",
			query: "?topic=*:*",
			want: map[string][]string{
				"*": []string{"*"},
			},
		},
		{
			desc:  "all topics and keys inferred",
			query: "",
			want: map[string][]string{
				"*": []string{"*"},
			},
		},
		{
			desc:    "invalid key value formatting",
			query:   "?topic=NodeDrain:*:*",
			wantErr: true,
		},
		{
			desc:    "invalid key value formatting no value",
			query:   "?topic=NodeDrain",
			wantErr: true,
		},
		{
			desc:  "single topic and key",
			query: "?topic=NodeDrain:*",
			want: map[string][]string{
				"NodeDrain": []string{"*"},
			},
		},
		{
			desc:  "single topic multiple keys",
			query: "?topic=NodeDrain:*&topic=NodeDrain:3caace09-f1f4-4d23-b37a-9ab5eb75069d",
			want: map[string][]string{
				"NodeDrain": []string{
					"*",
					"3caace09-f1f4-4d23-b37a-9ab5eb75069d",
				},
			},
		},
		{
			desc:  "multiple topics",
			query: "?topic=NodeRegister:*&topic=NodeDrain:3caace09-f1f4-4d23-b37a-9ab5eb75069d",
			want: map[string][]string{
				"NodeDrain": []string{
					"3caace09-f1f4-4d23-b37a-9ab5eb75069d",
				},
				"NodeRegister": []string{
					"*",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			raw := fmt.Sprintf("http://localhost:80/v1/events%s", tc.query)
			req, err := url.Parse(raw)
			require.NoError(t, err)

			got, err := parseEventTopics(req.Query())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
