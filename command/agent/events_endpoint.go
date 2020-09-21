package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/docker/docker/pkg/ioutils"
	"github.com/hashicorp/go-msgpack/codec"
	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/nomad/stream"
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
	// TODO plaintext or json
	resp.Header().Set("Content-Type", "application/json")

	s.parse(resp, req, &args.QueryOptions.Region, &args.QueryOptions)

	// Make the RPC
	var handler structs.StreamingRpcHandler
	var handlerErr error

	if server := s.agent.Server(); server != nil {
		handler, handlerErr = server.StreamingRpcHandler("Event.Stream")
	} else if client := s.agent.Client(); client != nil {
		handler, handlerErr = client.RemoteStreamingRpcHandler("Event.Stream")
	} else {
		handlerErr = fmt.Errorf("misconfigured connection")
	}

	if handlerErr != nil {
		return nil, CodedError(500, handlerErr.Error())
	}

	httpPipe, handlerPipe := net.Pipe()
	decoder := codec.NewDecoder(httpPipe, structs.MsgpackHandle)
	encoder := codec.NewEncoder(httpPipe, structs.MsgpackHandle)

	// Create a goroutine that closes the pipe if the connection closes
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	go func() {
		<-ctx.Done()
		httpPipe.Close()
	}()

	// Create an output that gets flushed on every write
	output := ioutils.NewWriteFlusher(resp)

	// create an error channel to handle errors
	errCh := make(chan HTTPCodedError, 2)

	go func() {
		defer cancel()

		// Send the request
		if err := encoder.Encode(args); err != nil {
			errCh <- CodedError(500, err.Error())
			return
		}

		for {
			select {
			case <-ctx.Done():
				errCh <- nil
				return
			default:
			}

			var res cstructs.StreamErrWrapper
			if err := decoder.Decode(&res); err != nil {
				errCh <- CodedError(500, err.Error())
				return
			}
			decoder.Reset(httpPipe)

			if err := res.Error; err != nil {
				if err.Code != nil {
					errCh <- CodedError(int(*err.Code), err.Error())
					return
				}
			}

			if _, err := io.Copy(output, bytes.NewReader(res.Payload)); err != nil {
				errCh <- CodedError(500, err.Error())
				return
			}
		}
	}()

	handler(handlerPipe)
	cancel()
	codedErr := <-errCh

	if codedErr != nil &&
		(codedErr == io.EOF ||
			strings.Contains(codedErr.Error(), "closed") ||
			strings.Contains(codedErr.Error(), "EOF")) {
		codedErr = nil
	}

	return nil, codedErr
}

func parseEventTopics(query url.Values) (map[stream.Topic][]string, error) {
	raw, ok := query["topic"]
	if !ok {
		return allTopics(), nil
	}
	topics := make(map[stream.Topic][]string)

	for _, topic := range raw {
		k, v, err := parseTopic(topic)
		if err != nil {
			return nil, fmt.Errorf("error parsing topics: %w", err)
		}

		if topics[stream.Topic(k)] == nil {
			topics[stream.Topic(k)] = []string{v}
		} else {
			topics[stream.Topic(k)] = append(topics[stream.Topic(k)], v)
		}
	}
	return topics, nil
}

func parseTopic(topic string) (string, string, error) {
	parts := strings.Split(topic, ":")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("Invalid key value pair for topic, topic: %s", topic)
	}
	return parts[0], parts[1], nil
}

func allTopics() map[stream.Topic][]string {
	return map[stream.Topic][]string{"*": []string{"*"}}
}
