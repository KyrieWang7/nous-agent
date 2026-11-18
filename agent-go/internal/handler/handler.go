package handler

import (
	"net/http"

	"github.com/KyrieWang7/nous-agent/agent-go/internal/router"
	"github.com/KyrieWang7/nous-agent/agent-go/internal/service"
)

type Handler struct{ app *service.Service }

func New(app *service.Service) *Handler { return &Handler{app: app} }
func (s *Handler) Endpoints() router.Endpoints {
	return router.Endpoints{Plugins: s.plugins, ChangePlugins: s.changePlugins, ReloadPlugins: s.reloadPlugins,
		EnqueueInput: s.enqueueInput, ListInputs: s.listInputs, GetInput: s.getInput, CancelInput: s.cancelInput, ChangeInput: s.changeInput,
		SendSubagentInput: s.sendSubagentInput, SubagentInputs: s.subagentInputs, ListSubagents: s.listSubagents, GetSubagent: s.getSubagent, CancelSubagent: s.cancelSubagent, WaitSubagent: s.waitSubagent, ContinueSubagent: s.continueSubagent,
		Health:         s.health,
		CreateThread:   s.createThread,
		SearchThreads:  s.searchThreads,
		GetThread:      s.getThread,
		PatchThread:    s.patchThread,
		DeleteThread:   s.deleteThread,
		GetState:       s.getState,
		PostState:      s.postState,
		GetHistory:     s.getHistory,
		CreateRun:      s.createRun,
		ListRuns:       s.listRuns,
		GetRun:         s.getRun,
		ForkRun:        s.forkRun,
		ReconnectRun:   s.reconnectRun,
		RawEvents:      s.rawEvents,
		CancelRun:      s.cancelRun,
		GetQuestion:    s.getQuestion,
		AnswerQuestion: s.answerQuestion,
	}
}
func (s *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
