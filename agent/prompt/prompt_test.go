package prompt

import (
	"strings"
	"testing"
)

// --- Text ---

func TestText_String(t *testing.T) {
	txt := Text("You are a helpful assistant.")
	if txt.String() != "You are a helpful assistant." {
		t.Errorf("expected exact string, got %q", txt.String())
	}
}

func TestText_Empty(t *testing.T) {
	txt := Text("")
	if txt.String() != "" {
		t.Errorf("expected empty string, got %q", txt.String())
	}
}

func TestText_ImplementsInstructions(t *testing.T) {
	var _ Instructions = Text("test")
}

// --- RISEN ---

func TestRISEN_AllFields(t *testing.T) {
	r := RISEN{
		Role:         "Travel planner",
		Instructions: "Help users plan trips",
		Steps:        "1) Ask preferences 2) Suggest destinations",
		EndGoal:      "A practical travel plan",
		Narrowing:    "Europe only, under 7 days",
	}
	s := r.String()

	checks := []struct {
		label string
		want  string
	}{
		{"Role", "Role: Travel planner"},
		{"Instructions", "Instructions: Help users plan trips"},
		{"Steps", "Steps: 1) Ask preferences 2) Suggest destinations"},
		{"EndGoal", "End goal: A practical travel plan"},
		{"Narrowing", "Narrowing: Europe only, under 7 days"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("%s: expected %q in output, got:\n%s", c.label, c.want, s)
		}
	}
}

func TestRISEN_EmptyFieldsOmitted(t *testing.T) {
	r := RISEN{
		Role:    "Assistant",
		EndGoal: "Help the user",
	}
	s := r.String()

	if !strings.Contains(s, "Role: Assistant") {
		t.Errorf("expected Role in output, got:\n%s", s)
	}
	if !strings.Contains(s, "End goal: Help the user") {
		t.Errorf("expected End goal in output, got:\n%s", s)
	}
	// Empty fields should not appear.
	for _, absent := range []string{"Instructions:", "Steps:", "Narrowing:"} {
		if strings.Contains(s, absent) {
			t.Errorf("expected %q to be absent, got:\n%s", absent, s)
		}
	}
}

func TestRISEN_AllEmpty(t *testing.T) {
	r := RISEN{}
	if r.String() != "" {
		t.Errorf("expected empty string for zero-value RISEN, got %q", r.String())
	}
}

func TestRISEN_FieldOrder(t *testing.T) {
	r := RISEN{
		Role:         "A",
		Instructions: "B",
		Steps:        "C",
		EndGoal:      "D",
		Narrowing:    "E",
	}
	s := r.String()

	idxRole := strings.Index(s, "Role:")
	idxInst := strings.Index(s, "Instructions:")
	idxStep := strings.Index(s, "Steps:")
	idxGoal := strings.Index(s, "End goal:")
	idxNarr := strings.Index(s, "Narrowing:")

	if !(idxRole < idxInst && idxInst < idxStep && idxStep < idxGoal && idxGoal < idxNarr) {
		t.Errorf("fields not in expected order: Role(%d) < Instructions(%d) < Steps(%d) < EndGoal(%d) < Narrowing(%d)",
			idxRole, idxInst, idxStep, idxGoal, idxNarr)
	}
}

func TestRISEN_ImplementsInstructions(t *testing.T) {
	var _ Instructions = RISEN{}
}

// --- COSTAR ---

func TestCOSTAR_AllFields(t *testing.T) {
	c := COSTAR{
		Context:   "Customer support for SaaS",
		Objective: "Resolve issues quickly",
		Style:     "Clear and structured",
		Tone:      "Friendly and patient",
		Audience:  "Non-technical users",
		Response:  "Under 3 paragraphs",
	}
	s := c.String()

	checks := []struct {
		label string
		want  string
	}{
		{"Context", "Context: Customer support for SaaS"},
		{"Objective", "Objective: Resolve issues quickly"},
		{"Style", "Style: Clear and structured"},
		{"Tone", "Tone: Friendly and patient"},
		{"Audience", "Audience: Non-technical users"},
		{"Response", "Response format: Under 3 paragraphs"},
	}
	for _, ch := range checks {
		if !strings.Contains(s, ch.want) {
			t.Errorf("%s: expected %q in output, got:\n%s", ch.label, ch.want, s)
		}
	}
}

func TestCOSTAR_EmptyFieldsOmitted(t *testing.T) {
	c := COSTAR{
		Objective: "Answer questions",
		Tone:      "Professional",
	}
	s := c.String()

	if !strings.Contains(s, "Objective: Answer questions") {
		t.Errorf("expected Objective in output, got:\n%s", s)
	}
	if !strings.Contains(s, "Tone: Professional") {
		t.Errorf("expected Tone in output, got:\n%s", s)
	}
	for _, absent := range []string{"Context:", "Style:", "Audience:", "Response format:"} {
		if strings.Contains(s, absent) {
			t.Errorf("expected %q to be absent, got:\n%s", absent, s)
		}
	}
}

func TestCOSTAR_AllEmpty(t *testing.T) {
	c := COSTAR{}
	if c.String() != "" {
		t.Errorf("expected empty string for zero-value COSTAR, got %q", c.String())
	}
}

func TestCOSTAR_FieldOrder(t *testing.T) {
	c := COSTAR{
		Context:   "A",
		Objective: "B",
		Style:     "C",
		Tone:      "D",
		Audience:  "E",
		Response:  "F",
	}
	s := c.String()

	idxCtx := strings.Index(s, "Context:")
	idxObj := strings.Index(s, "Objective:")
	idxSty := strings.Index(s, "Style:")
	idxTon := strings.Index(s, "Tone:")
	idxAud := strings.Index(s, "Audience:")
	idxRes := strings.Index(s, "Response format:")

	if !(idxCtx < idxObj && idxObj < idxSty && idxSty < idxTon && idxTon < idxAud && idxAud < idxRes) {
		t.Errorf("fields not in expected order: Context(%d) < Objective(%d) < Style(%d) < Tone(%d) < Audience(%d) < Response(%d)",
			idxCtx, idxObj, idxSty, idxTon, idxAud, idxRes)
	}
}

func TestCOSTAR_ImplementsInstructions(t *testing.T) {
	var _ Instructions = COSTAR{}
}

// --- APE ---

func TestAPE_AllFields(t *testing.T) {
	a := APE{
		Action:      "Search the knowledge base and answer questions",
		Purpose:     "Help support agents resolve tickets faster",
		Expectation: "Concise answers with source links",
	}
	s := a.String()

	checks := []struct {
		label, want string
	}{
		{"Action", "Action: Search the knowledge base and answer questions"},
		{"Purpose", "Purpose: Help support agents resolve tickets faster"},
		{"Expectation", "Expectation: Concise answers with source links"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("%s: expected %q in output, got:\n%s", c.label, c.want, s)
		}
	}
}

func TestAPE_EmptyFieldsOmitted(t *testing.T) {
	a := APE{Action: "Do stuff"}
	s := a.String()

	if !strings.Contains(s, "Action: Do stuff") {
		t.Errorf("expected Action in output, got:\n%s", s)
	}
	for _, absent := range []string{"Purpose:", "Expectation:"} {
		if strings.Contains(s, absent) {
			t.Errorf("expected %q to be absent, got:\n%s", absent, s)
		}
	}
}

func TestAPE_AllEmpty(t *testing.T) {
	a := APE{}
	if a.String() != "" {
		t.Errorf("expected empty string for zero-value APE, got %q", a.String())
	}
}

func TestAPE_FieldOrder(t *testing.T) {
	a := APE{Action: "A", Purpose: "B", Expectation: "C"}
	s := a.String()

	if !(strings.Index(s, "Action:") < strings.Index(s, "Purpose:") &&
		strings.Index(s, "Purpose:") < strings.Index(s, "Expectation:")) {
		t.Errorf("fields not in expected order in:\n%s", s)
	}
}

func TestAPE_ImplementsInstructions(t *testing.T) {
	var _ Instructions = APE{}
}

// --- TRACE ---

func TestTRACE_AllFields(t *testing.T) {
	tr := TRACE{
		Task:    "Code review assistant",
		Request: "Review pull requests for bugs and style",
		Action:  "Read the diff, identify issues, suggest fixes",
		Context: "Go microservice using pgx for Postgres",
		Example: "Input: bad SQL\nOutput: use parameterized queries",
	}
	s := tr.String()

	checks := []struct {
		label, want string
	}{
		{"Task", "Task: Code review assistant"},
		{"Request", "Request: Review pull requests for bugs and style"},
		{"Action", "Action: Read the diff, identify issues, suggest fixes"},
		{"Context", "Context: Go microservice using pgx for Postgres"},
		{"Example", "Example: Input: bad SQL"},
	}
	for _, c := range checks {
		if !strings.Contains(s, c.want) {
			t.Errorf("%s: expected %q in output, got:\n%s", c.label, c.want, s)
		}
	}
}

func TestTRACE_EmptyFieldsOmitted(t *testing.T) {
	tr := TRACE{Task: "Summarize", Context: "Legal documents"}
	s := tr.String()

	if !strings.Contains(s, "Task: Summarize") {
		t.Errorf("expected Task in output, got:\n%s", s)
	}
	if !strings.Contains(s, "Context: Legal documents") {
		t.Errorf("expected Context in output, got:\n%s", s)
	}
	for _, absent := range []string{"Request:", "Action:", "Example:"} {
		if strings.Contains(s, absent) {
			t.Errorf("expected %q to be absent, got:\n%s", absent, s)
		}
	}
}

func TestTRACE_AllEmpty(t *testing.T) {
	tr := TRACE{}
	if tr.String() != "" {
		t.Errorf("expected empty string for zero-value TRACE, got %q", tr.String())
	}
}

func TestTRACE_FieldOrder(t *testing.T) {
	tr := TRACE{Task: "A", Request: "B", Action: "C", Context: "D", Example: "E"}
	s := tr.String()

	if !(strings.Index(s, "Task:") < strings.Index(s, "Request:") &&
		strings.Index(s, "Request:") < strings.Index(s, "Action:") &&
		strings.Index(s, "Action:") < strings.Index(s, "Context:") &&
		strings.Index(s, "Context:") < strings.Index(s, "Example:")) {
		t.Errorf("fields not in expected order in:\n%s", s)
	}
}

func TestTRACE_ImplementsInstructions(t *testing.T) {
	var _ Instructions = TRACE{}
}
