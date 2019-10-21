package teamserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
	"gocloud.dev/pubsub"

	"github.com/golang/protobuf/proto"
	"github.com/kcarretto/paragon/api/codec"
	"github.com/kcarretto/paragon/api/events"
	"github.com/kcarretto/paragon/ent"
)

// Server handles c2 messages and replies with new tasks for the c2 to send out.
type Server struct {
	Log         *zap.Logger
	EntClient   *ent.Client
	QueuedTopic *pubsub.Topic
}

type rawTask struct {
	Content   string `json:"content"`
	SessionID string `json:"sessionID"`
	TargetID  int    `json:"targetID"`
}

type rawTarget struct {
	Name        string `json:"name"`
	MachineUUID string `json:"machineUUID"`
	Hostname    string `json:"hostname"`
	PrimaryIP   string `json:"primaryIP"`
	PrimaryMAC  string `json:"primaryMAC"`
}

type iDStruct struct {
	ID int `json:"id"`
}

type messageData struct {
	Data      []byte `json:"data"`
	MessageID string `json:"messageId"`
}

type pubSubMessage struct {
	Message      messageData `json:"message"`
	Subscription string      `json:"subscription"`
}

func (srv *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	data := map[string]interface{}{
		"status": "OK",
	}
	resp, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "failed to marshal the json for the status", http.StatusInternalServerError)
		return
	}

	if _, err = w.Write(resp); err != nil {
		http.Error(w, fmt.Sprintf("Failed to write response data: %s", err.Error()), http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
}

func (srv *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var message iDStruct
		err := decoder.Decode(&message)
		if err != nil {
			http.Error(w, "improper id struct sent", http.StatusBadRequest)
			return
		}
		ctx := context.Background()
		task, err := srv.EntClient.Task.Get(ctx, message.ID)

		data := map[string]interface{}{
			"id":            task.ID,
			"queueTime":     task.QueueTime.Unix(),
			"claimTime":     task.ClaimTime.Unix(),
			"execStartTime": task.ExecStartTime.Unix(),
			"execStopTime":  task.ExecStopTime.Unix(),
			"content":       task.Content,
			"output":        task.Output,
			"error":         task.Error,
			"sessionID":     task.SessionID,
		}
		resp, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "failed to marshal the json for the task", http.StatusInternalServerError)
			return
		}
		if _, err = w.Write(resp); err != nil {
			http.Error(w, "Failed to write response data", http.StatusInternalServerError)
		}
		w.Header().Set("Content-Type", "application/json")
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

func (srv *Server) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var message iDStruct
		err := decoder.Decode(&message)
		if err != nil {
			http.Error(w, "improper id struct sent", http.StatusBadRequest)
			return
		}
		ctx := context.Background()
		target, err := srv.EntClient.Target.Get(ctx, message.ID)

		data := map[string]interface{}{
			"id":          target.ID,
			"name":        target.Name,
			"machineUUID": target.MachineUUID,
			"primaryIP":   target.PrimaryIP,
			"primaryMAC":  target.PrimaryMAC,
			"hostname":    target.Hostname,
			"lastSeen":    target.LastSeen,
		}
		resp, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "failed to marshal the json for the target", http.StatusInternalServerError)
			return
		}
		if _, err = w.Write(resp); err != nil {
			http.Error(w, "Failed to write response data", http.StatusInternalServerError)
		}
		w.Header().Set("Content-Type", "application/json")
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

func (srv *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	targets, err := srv.EntClient.Target.Query().All(ctx)
	if err != nil {
		http.Error(w, "failed to query the ent db for targets", http.StatusInternalServerError)
		return
	}
	var targetIDs []int
	for _, target := range targets {
		targetIDs = append(targetIDs, target.ID)
	}
	data := map[string]interface{}{
		"ids": targetIDs,
	}
	resp, err := json.Marshal(data)
	if err != nil {
		http.Error(w, "failed to marshal the json for the ids", http.StatusInternalServerError)
		return
	}
	if _, err = w.Write(resp); err != nil {
		http.Error(w, "Failed to write response data", http.StatusInternalServerError)
	}
	w.Header().Set("Content-Type", "application/json")
}

func (srv *Server) handleListTasksForTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var message iDStruct
		err := decoder.Decode(&message)
		if err != nil {
			http.Error(w, "improper id struct sent", http.StatusBadRequest)
			return
		}
		ctx := context.Background()
		target, err := srv.EntClient.Target.Get(ctx, message.ID)
		if err != nil {
			http.Error(w, "failed to lookup target with given id", http.StatusBadRequest)
			return
		}

		tasks, err := target.QueryTasks().All(ctx)
		var taskIDs []int
		for _, task := range tasks {
			taskIDs = append(taskIDs, task.ID)
		}
		data := map[string]interface{}{
			"ids": taskIDs,
		}
		resp, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "failed to marshal the json for the ids", http.StatusInternalServerError)
			return
		}
		if _, err = w.Write(resp); err != nil {
			http.Error(w, "Failed to write response data", http.StatusInternalServerError)
		}
		w.Header().Set("Content-Type", "application/json")
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

func (srv *Server) handleTaskClaimed(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var message pubSubMessage
		err := decoder.Decode(&message)
		if err != nil {
			http.Error(w, "improper pubsub message json sent", http.StatusBadRequest)
			return
		}
		var event events.TaskClaimed
		if err := proto.Unmarshal(message.Message.Data, &event); err != nil {
			srv.Log.Error("failed to parse protobuf", zap.Error(err))
		}
		ctx := context.Background()
		err = srv.taskClaimed(ctx, event)
		if err != nil {
			http.Error(w, "an error occured in updating the claimed task", http.StatusInternalServerError)
			return
		}
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

func (srv *Server) handleTaskExecuted(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var message pubSubMessage
		err := decoder.Decode(&message)
		if err != nil {
			http.Error(w, "improper pubsub message json sent", http.StatusBadRequest)
			return
		}
		var event events.TaskExecuted
		if err := proto.Unmarshal(message.Message.Data, &event); err != nil {
			srv.Log.Error("failed to parse protobuf", zap.Error(err))
		}
		ctx := context.Background()
		err = srv.taskExecuted(ctx, event)
		if err != nil {
			http.Error(w, "an error occured in updating the executed task", http.StatusInternalServerError)
			return
		}
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

func (srv *Server) handleMakeTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var t rawTarget
		err := decoder.Decode(&t)
		if err != nil {
			http.Error(w, "improper task json sent", http.StatusBadRequest)
			return
		}
		ctx := context.Background()
		newTarget, err := srv.EntClient.Target.
			Create().
			SetName(t.Name).
			SetMachineUUID(t.MachineUUID).
			SetHostname(t.Hostname).
			SetPrimaryIP(t.PrimaryIP).
			SetPrimaryMAC(t.PrimaryMAC).
			Save(ctx)
		if err != nil {
			http.Error(w, "unable to create new target", http.StatusInternalServerError)
			return
		}
		resp := iDStruct{
			ID: newTarget.ID,
		}
		respBody, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "unable to create response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

func (srv *Server) handleQueueTask(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		decoder := json.NewDecoder(r.Body)
		var t rawTask
		err := decoder.Decode(&t)
		if err != nil {
			http.Error(w, "improper task json sent", http.StatusBadRequest)
			return
		}
		ctx := context.Background()
		target, err := srv.EntClient.Target.Get(ctx, t.TargetID)
		if err != nil {
			http.Error(w, "improper target id given", http.StatusBadRequest)
			return
		}
		newTask, err := srv.EntClient.Task.
			Create().
			SetContent(t.Content).
			SetSessionID(t.SessionID).
			SetTarget(target).
			Save(ctx)
		if err != nil {
			http.Error(w, "unable to create new task", http.StatusInternalServerError)
			return
		}
		resp := iDStruct{
			ID: newTask.ID,
		}
		respBody, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "unable to create response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(respBody)
		if err := srv.queueTask(ctx, newTask); err != nil {
			http.Error(w, "unable to queue task to topic", http.StatusInternalServerError)
			return
		}
		return
	}

	http.Error(w, "404 not found.", http.StatusNotFound)
}

// Run begins the handlers for processing the subscriptions to the `tasks.claimed` and `tasks.executed` topics
func (srv *Server) Run() {
	http.HandleFunc("/status", srv.handleStatus)

	http.HandleFunc("/events/tasks/claimed", srv.handleTaskClaimed)
	http.HandleFunc("/events/tasks/executed", srv.handleTaskExecuted)

	http.HandleFunc("/queueTask", srv.handleQueueTask)
	http.HandleFunc("/getTask", srv.handleGetTask)
	http.HandleFunc("/getTarget", srv.handleGetTarget)
	http.HandleFunc("/listTargets", srv.handleListTargets)
	http.HandleFunc("/listTasksForTarget", srv.handleListTasksForTarget)
	if err := http.ListenAndServe("0.0.0.0:80", nil); err != nil {
		panic(err)
	}
}

// QueueTask sends a given task (and some associated target data) to the `tasks.queued` topic
func (srv *Server) queueTask(ctx context.Context, task *ent.Task) error {
	target := task.QueryTarget().FirstX(ctx)
	targetID := strconv.Itoa(target.ID)
	agentMetadata := codec.AgentMetadata{
		AgentID:     targetID,
		MachineUUID: target.MachineUUID,
		SessionID:   task.SessionID,
		Hostname:    target.Hostname,
		PrimaryIP:   target.PrimaryIP,
		PrimaryMAC:  target.PrimaryMAC,
	}
	taskID := strconv.Itoa(task.ID)
	event := events.TaskQueued{
		Id:      taskID,
		Content: task.Content,
		Filter:  &agentMetadata,
	}
	body, err := proto.Marshal(&event)
	if err != nil {
		return err
	}
	err = srv.QueuedTopic.Send(ctx, &pubsub.Message{
		Body: body,
	})
	if err != nil {
		return err
	}
	return nil
}

func (srv *Server) taskClaimed(ctx context.Context, event events.TaskClaimed) error {
	taskID, err := strconv.Atoi(event.GetId())
	if err != nil {
		return err
	}
	task, err := srv.EntClient.Task.Get(ctx, taskID)
	if err != nil {
		return err
	}
	task, err = task.Update().
		SetClaimTime(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}
	target := task.QueryTarget().FirstX(ctx)
	target, err = target.Update().
		SetLastSeen(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (srv *Server) taskExecuted(ctx context.Context, event events.TaskExecuted) error {
	taskID, err := strconv.Atoi(event.GetId())
	if err != nil {
		return err
	}
	task, err := srv.EntClient.Task.Get(ctx, taskID)
	if err != nil {
		return err
	}
	execStartTime := time.Unix(event.GetExecStartTime(), 0)
	execStopTime := time.Unix(event.GetExecStopTime(), 0)
	output := event.GetOutput()
	task, err = task.Update().
		SetExecStartTime(execStartTime).
		SetExecStopTime(execStopTime).
		SetOutput(output).
		SetError(event.GetError()).
		Save(ctx)
	if err != nil {
		return err
	}
	target := task.QueryTarget().FirstX(ctx)
	target, err = target.Update().
		SetLastSeen(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}
	return nil
}
