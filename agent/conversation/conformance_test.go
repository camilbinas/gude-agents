package conversation_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/camilbinas/gude-agents/agent"
	conv "github.com/camilbinas/gude-agents/agent/conversation"
	"github.com/camilbinas/gude-agents/agent/conversation/internal/conformance"
)

func TestAppendConformanceInMemory(t *testing.T) { conformance.RunAppend(t, conv.NewInMemory()) }

func TestAppendConcurrentCASOneWinner(t *testing.T) {
	s := conv.NewInMemory()
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Append(context.Background(), "race", []agent.Message{conformance.Msg(fmt.Sprint(i))}, 0)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	wins := 0
	for err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, agent.ErrConversationConflict) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("wins=%d", wins)
	}
}

func TestContextStateConformanceInMemory(t *testing.T) {
	conformance.RunContextState(t, conv.NewInMemory())
}
