package agent

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/hashicorp/nomad/nomad/structs"
)

func (s *HTTPServer) EventStream(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	query := req.URL.Query()

	indexStr := query.Get("index")
	if indexStr == "" {
		indexStr = "0"
	}
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return nil, CodedError(400, fmt.Sprintf("Unable to parse index: %v", err))
	}

	follow := false
	fstr := req.URL.Query().Get("follow")
	if fstr != "" {
		f, err := strconv.ParseBool(fstr)
		if err != nil {
			return nil, CodedError(400, fmt.Sprintf("Unknown option for follow: %v", err))
		}
		follow = f
	}

	topics, err := parseEventTopics(query)
	if err != nil {
		return nil, CodedError(400, fmt.Sprintf("Invalid topic query: %v", err))
	}

	args := &structs.EventStreamRequest{
		Topics: topics,
		Index:  index,
		Follow: follow,
	}
	s.parse(resp, req, &args.QueryOptions.Region, &args.QueryOptions)

	return nil, nil
}

func parseEventTopics(query url.Values) (map[string][]string, error) {
	raw, ok := query["topic"]
	if !ok {
		return allTopics(), nil
	}
	topics := make(map[string][]string)

	for _, topic := range raw {
		k, v, err := parseTopic(topic)
		if err != nil {
			return nil, fmt.Errorf("error parsing topics: %w", err)
		}

		if topics[k] == nil {
			topics[k] = []string{v}
		} else {
			topics[k] = append(topics[k], v)
		}
	}
	return topics, nil
}

func parseTopic(topic string) (string, string, error) {
	parts := strings.Split(topic, ":")
	if len(parts) > 2 {
		return "", "", fmt.Errorf("Invalid key value pair for topic, topic: %s", topic)
	}
	return parts[0], parts[1], nil
}

func allTopics() map[string][]string {
	return map[string][]string{"*": []string{"*"}}
}
