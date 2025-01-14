package test

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/utils"
)

type agentClient struct {
	mu   sync.Mutex
	conn *websocket.Conn

	registered              atomic.Int32
	roomAvailability        atomic.Int32
	roomJobs                atomic.Int32
	participantAvailability atomic.Int32
	participantJobs         atomic.Int32

	done chan struct{}
}

func newAgentClient(token string) (*agentClient, error) {
	host := fmt.Sprintf("ws://localhost:%d", defaultServerPort)
	u, err := url.Parse(host + "/agent")
	if err != nil {
		return nil, err
	}
	requestHeader := make(http.Header)
	requestHeader.Set("Authorization", "Bearer "+token)

	connectUrl := u.String()
	conn, _, err := websocket.DefaultDialer.Dial(connectUrl, requestHeader)
	if err != nil {
		return nil, err
	}

	return &agentClient{
		conn: conn,
		done: make(chan struct{}),
	}, nil
}

func (c *agentClient) Run(jobType livekit.JobType) (err error) {
	go c.read()

	workerID := utils.NewGuid("W_")

	switch jobType {
	case livekit.JobType_JT_ROOM:
		err = c.write(&livekit.WorkerMessage{
			Message: &livekit.WorkerMessage_Register{
				Register: &livekit.RegisterWorkerRequest{
					Type:     livekit.JobType_JT_ROOM,
					WorkerId: workerID,
					Version:  "version",
					Name:     "name",
				},
			},
		})

	case livekit.JobType_JT_PUBLISHER:
		err = c.write(&livekit.WorkerMessage{
			Message: &livekit.WorkerMessage_Register{
				Register: &livekit.RegisterWorkerRequest{
					Type:     livekit.JobType_JT_PUBLISHER,
					WorkerId: workerID,
					Version:  "version",
					Name:     "name",
				},
			},
		})
	}

	return err
}

func (c *agentClient) read() {
	for {
		select {
		case <-c.done:
			return
		default:
			_, b, err := c.conn.ReadMessage()
			if err != nil {
				return
			}

			msg := &livekit.ServerMessage{}
			if err = proto.Unmarshal(b, msg); err != nil {
				return
			}

			switch m := msg.Message.(type) {
			case *livekit.ServerMessage_Assignment:
				go c.handleAssignment(m.Assignment)
			case *livekit.ServerMessage_Availability:
				go c.handleAvailability(m.Availability)
			case *livekit.ServerMessage_Register:
				go c.handleRegister(m.Register)
			}
		}
	}
}

func (c *agentClient) handleAssignment(req *livekit.JobAssignment) {
	if req.Job.Type == livekit.JobType_JT_ROOM {
		c.roomJobs.Inc()
	} else {
		c.participantJobs.Inc()
	}
}

func (c *agentClient) handleAvailability(req *livekit.AvailabilityRequest) {
	if req.Job.Type == livekit.JobType_JT_ROOM {
		c.roomAvailability.Inc()
	} else {
		c.participantAvailability.Inc()
	}

	c.write(&livekit.WorkerMessage{
		Message: &livekit.WorkerMessage_Availability{
			Availability: &livekit.AvailabilityResponse{
				JobId:     req.Job.Id,
				Available: true,
			},
		},
	})
}

func (c *agentClient) handleRegister(req *livekit.RegisterWorkerResponse) {
	c.registered.Inc()
}

func (c *agentClient) write(msg *livekit.WorkerMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	b, err := proto.Marshal(msg)
	if err != nil {
		return err
	}

	return c.conn.WriteMessage(websocket.BinaryMessage, b)
}

func (c *agentClient) close() {
	close(c.done)
	_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = c.conn.Close()
}
