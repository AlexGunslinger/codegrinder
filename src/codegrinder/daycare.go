package main

import (
	"crypto/hmac"
	"net/http"
	"strings"
	"time"

	"github.com/go-martini/martini"
	"github.com/gorilla/websocket"
)

const MaxDaycareRequestAge = time.Minute

type DaycareRequest struct {
	Problem *Problem `json:"problem,omitempty"`
	Commit  *Commit  `json:"commit,omitempty"`
	Stdin   string   `json:"stdin,omitempty"`
}

type DaycareResponse struct {
	Commit *Commit       `json:"commit,omitempty"`
	Event  *EventMessage `json:"event,omitempty"`
}

func SocketProblemTypeAction(w http.ResponseWriter, r *http.Request, params martini.Params) {
	problemType, exists := problemTypes[params["problem_type"]]
	if !exists {
		loggedHTTPErrorf(w, http.StatusNotFound, "problem type %q not found", params["problem_type"])
		return
	}
	action, exists := problemType.Actions[params["action"]]
	if !exists {
		loggedHTTPErrorf(w, http.StatusNotFound, "action %q not defined from problem type %s", params["action"], params["problem_type"])
		return
	}

	// get a websocket
	socket, err := websocket.Upgrade(w, r, nil, 1024, 1024)
	if err != nil {
		loggedHTTPErrorf(w, http.StatusBadRequest, "websocket error: %v", err)
		return
	}
	defer socket.Close()

	// get the first message
	req := new(DaycareRequest)
	if err := socket.ReadJSON(req); err != nil {
		loge.Printf("error reading first request message: %v", err)
		return
	}
	if req.Problem == nil {
		loge.Printf("first request message must include the problem")
		return
	}
	problem := req.Problem
	if req.Commit == nil {
		loge.Printf("first request message must include the commit")
		return
	}
	commit := req.Commit

	// check problem signature
	if problem.Timestamp == nil {
		loge.Printf("problem must have a valid timestamp")
		return
	}
	age := time.Since(*problem.Timestamp)
	if age < 0 {
		age = -age
	}
	if age > MaxDaycareRequestAge {
		loge.Printf("problem signature is %v off, cannot be more than %v", age, MaxDaycareRequestAge)
		return
	}
	if problem.Signature == "" {
		loge.Printf("problem must be signed")
		return
	}
	problemSig := problem.computeSignature(Config.DaycareSecret)
	if !hmac.Equal([]byte(problem.Signature), []byte(problemSig)) {
		loge.Printf("problem signature mismatch: found %s but expected %s", problem.Signature, problemSig)
		return
	}

	// check commit signature
	if commit.ProblemSignature != problemSig {
		loge.Printf("commit says problem signature is %s, but it is actually %s", commit.ProblemSignature, problemSig)
		return
	}
	if commit.Timestamp == nil {
		loge.Printf("commit must have a valid timestamp")
		return
	}
	age = time.Since(*commit.Timestamp)
	if age < 0 {
		age = -age
	}
	if age > MaxDaycareRequestAge {
		loge.Printf("commit signature is %v off, cannot be more than %v", age, MaxDaycareRequestAge)
		return
	}
	if commit.Signature == "" {
		loge.Printf("commit must be signed")
		return
	}
	if !commit.Closed {
		loge.Printf("commit must be closed")
		return
	}
	if commit.Action != params["action"] {
		loge.Printf("commit says action is %s, but request says %s", commit.Action, params["action"])
		return
	}
	commitSig := commit.computeSignature(Config.DaycareSecret)
	if !hmac.Equal([]byte(commit.Signature), []byte(commitSig)) {
		loge.Printf("commit signature mismatch: found %s but expected %s", commit.Signature, commitSig)
		return
	}

	// prepare the problem step
	if commit.ProblemStepNumber < 0 || commit.ProblemStepNumber >= len(problem.Steps) {
		loge.Printf("commit refers to step number that does not exist: %d", commit.ProblemStepNumber)
		return
	}
	step := problem.Steps[commit.ProblemStepNumber]
	files := make(map[string]string)
	for name, contents := range step.Files {
		files[name] = contents
	}
	for name, contents := range commit.Files {
		// TODO: filter commit files?
		if len(strings.Split(name, "/")) == 1 {
			files[name] = contents
		}
	}

	// launch a nanny process
	logi.Printf("launching container for nanny-%s", commitSig)
	n, err := NewNanny(problemType.Image, "nanny-"+commitSig)
	if err != nil {
		loge.Printf("error creating nanny: %v", err)
		return
	}

	// start a listener
	finished := make(chan struct{})
	go func() {
		for event := range n.Events {
			// feed events back to client
			res := &DaycareResponse{Event: event}
			if err := socket.WriteJSON(res); err != nil {
				loge.Printf("error writing event JSON: %v", err)
			}
		}
		finished <- struct{}{}
	}()

	// grade the problem
	rc := NewReportCard()
	r.ParseForm()
	action.handler(n, r.Form["args"], problem.Options, files)
	dump(rc)

	// shutdown the nanny
	if err := n.Shutdown(); err != nil {
		loge.Printf("nanny shutdown error: %v", err)
	}

	// wait for listener to finish
	close(n.Events)
	<-finished

	// send the final commit back to the client
	commit.Transcript = n.Transcript
	commit.ReportCard = rc

	// compute the score for this step on a scale of 0.0 to 1.0
	if rc.Passed {
		// award full credit for this step
		commit.Score = 1.0
	} else if len(rc.Results) == 0 {
		// no results? that's a fail...
		commit.Score = 0.0
	} else {
		// compute partial credit for this step
		passed := 0
		for _, elt := range rc.Results {
			if elt.Outcome == "passed" {
				passed++
			}
		}
		commit.Score = float64(passed) / float64(len(rc.Results))
	}
	now := time.Now()
	commit.Timestamp = &now
	commit.Signature = commit.computeSignature(Config.DaycareSecret)

	res := &DaycareResponse{Commit: commit}
	if err := socket.WriteJSON(res); err != nil {
		loge.Printf("error writing final commit JSON: %v", err)
		return
	}
}