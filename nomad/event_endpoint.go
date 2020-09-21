package nomad

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/hashicorp/go-msgpack/codec"
	sframer "github.com/hashicorp/nomad/client/lib/streamframer"
	cstructs "github.com/hashicorp/nomad/client/structs"
	"github.com/hashicorp/nomad/helper"
	"github.com/hashicorp/nomad/nomad/stream"
	"github.com/hashicorp/nomad/nomad/structs"
)

type Event struct {
	srv *Server
}

func (e *Event) register() {
	e.srv.streamingRpcs.Register("Event.Stream", e.stream)

}

func (e *Event) stream(conn io.ReadWriteCloser) {
	defer conn.Close()

	var args structs.EventStreamRequest
	decoder := codec.NewDecoder(conn, structs.MsgpackHandle)
	encoder := codec.NewEncoder(conn, structs.MsgpackHandle)

	if err := decoder.Decode(&args); err != nil {
		handleStreamResultError(err, helper.Int64ToPtr(500), encoder)
		return
	}

	// forward to appropriate region
	if args.Region != e.srv.config.Region {
		err := e.forwardStreamingRPC(args.Region, "Event.Stream", args, conn)
		if err != nil {
			handleStreamResultError(err, helper.Int64ToPtr(500), encoder)
		}
		return
	}

	// ACL check
	// TODO ACL checks need to be per topic
	if aclObj, err := e.srv.ResolveToken(args.AuthToken); err != nil {
		handleStreamResultError(err, nil, encoder)
		return
	} else if aclObj != nil && !aclObj.AllowAgentRead() {
		handleStreamResultError(structs.ErrPermissionDenied, helper.Int64ToPtr(403), encoder)
		return
	}

	subReq := &stream.SubscribeRequest{
		Topics: args.Topics,
		Index:  uint64(args.Index),
	}
	publisher, err := e.srv.State().EventPublisher()
	if err != nil {
		handleStreamResultError(err, helper.Int64ToPtr(500), encoder)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subscription, err := publisher.Subscribe(subReq)
	if err != nil {
		handleStreamResultError(err, helper.Int64ToPtr(500), encoder)
		return
	}

	frames := make(chan *sframer.StreamFrame, 32)
	errCh := make(chan error)
	var buf bytes.Buffer
	frameCodec := codec.NewEncoder(&buf, structs.JsonHandle)

	framer := sframer.NewStreamFramer(frames, 1*time.Second, 200*time.Millisecond, 1024)
	framer.Run()
	defer framer.Destroy()

	// goroutine to detect remote side closing
	go func() {
		if _, err := conn.Read(nil); err != nil {
			// One end of the pipe explicitly closed, exit
			cancel()
			return
		}
		select {
		case <-ctx.Done():
			return
		}
	}()

	initialOffset := int64(args.Index)
	go func() {
		defer framer.Destroy()
	LOOP:
		for {
			events, err := subscription.Next(ctx)
			if err != nil {
				select {
				case errCh <- err:
				case <-ctx.Done():
				}
				break LOOP
			}

			b, err := json.Marshal(events)
			if err != nil {
				select {
				case errCh <- err:
				case <-ctx.Done():
				}
				break LOOP
			}
			if err := framer.Send("", "stream", b, initialOffset); err != nil {
				select {
				case errCh <- err:
				case <-ctx.Done():
				}
				break LOOP
			}
		}
	}()

	var streamErr error
OUTER:
	for {
		select {
		case <-ctx.Done():
			break OUTER
		case frame, ok := <-frames:
			if !ok {
				select {
				case streamErr = <-errCh:
				default:
				}
				break OUTER
			}

			var resp cstructs.StreamErrWrapper
			// TODO plain text
			if err := frameCodec.Encode(frame); err != nil {
				streamErr = err
				break OUTER
			}

			resp.Payload = buf.Bytes()
			buf.Reset()

			if err := encoder.Encode(resp); err != nil {
				streamErr = err
				break OUTER
			}
			encoder.Reset(conn)
		}

	}

	if streamErr != nil {
		handleStreamResultError(streamErr, helper.Int64ToPtr(500), encoder)
		return
	}

}

func (e *Event) forwardStreamingRPC(region string, method string, args interface{}, in io.ReadWriteCloser) error {
	server, err := e.srv.findRegionServer(region)
	if err != nil {
		return err
	}

	return e.forwardStreamingRPCToServer(server, method, args, in)
}

func (e *Event) forwardStreamingRPCToServer(server *serverParts, method string, args interface{}, in io.ReadWriteCloser) error {
	srvConn, err := e.srv.streamingRpc(server, method)
	if err != nil {
		return err
	}
	defer srvConn.Close()

	outEncoder := codec.NewEncoder(srvConn, structs.MsgpackHandle)
	if err := outEncoder.Encode(args); err != nil {
		return err
	}

	structs.Bridge(in, srvConn)
	return nil
}
