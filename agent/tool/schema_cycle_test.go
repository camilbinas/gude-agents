package tool

import (
	"testing"
	"time"
)

// node is directly self-referential through a pointer field.
type node struct {
	Value string `json:"value"`
	Next  *node  `json:"next"`
}

// ringA and ringB form a mutual (indirect) cycle.
type ringA struct {
	B *ringB `json:"b"`
}
type ringB struct {
	A *ringA `json:"a"`
}

// selfSlice references itself through a slice element.
type selfSlice struct {
	Name     string      `json:"name"`
	Children []selfSlice `json:"children"`
}

func TestGenerateSchema_DirectCycleTerminates(t *testing.T) {
	done := make(chan map[string]any, 1)
	go func() { done <- GenerateSchema[node]() }()
	select {
	case schema := <-done:
		props := schema["properties"].(map[string]any)
		next, ok := props["next"].(map[string]any)
		if !ok {
			t.Fatalf("missing 'next' property: %v", props)
		}
		// On the cycle the self-reference collapses to an open object with no
		// further 'properties' descent.
		if next["type"] != "object" {
			t.Errorf("want cycle node emitted as object, got %v", next["type"])
		}
		if _, hasProps := next["properties"]; hasProps {
			t.Errorf("cycle node should not recurse into properties: %v", next)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GenerateSchema did not terminate on a self-referential type")
	}
}

func TestGenerateSchema_MutualCycleTerminates(t *testing.T) {
	done := make(chan struct{})
	go func() {
		_ = GenerateSchema[ringA]()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GenerateSchema did not terminate on a mutually recursive type")
	}
}

func TestGenerateSchema_SelfReferentialSliceTerminates(t *testing.T) {
	done := make(chan map[string]any, 1)
	go func() { done <- GenerateSchema[selfSlice]() }()
	select {
	case schema := <-done:
		props := schema["properties"].(map[string]any)
		children, ok := props["children"].(map[string]any)
		if !ok || children["type"] != "array" {
			t.Fatalf("want children as array, got %v", props["children"])
		}
		items := children["items"].(map[string]any)
		if items["type"] != "object" {
			t.Errorf("want cycle element as object, got %v", items["type"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("GenerateSchema did not terminate on a self-referential slice")
	}
}

func TestGenerateSchema_SiblingSameTypeStillExpands(t *testing.T) {
	// Two fields of the same (non-recursive) type must both expand fully; the
	// visited set must not treat the second sibling as a cycle.
	type leaf struct {
		X string `json:"x"`
	}
	type pair struct {
		First  leaf `json:"first"`
		Second leaf `json:"second"`
	}
	schema := GenerateSchema[pair]()
	props := schema["properties"].(map[string]any)
	for _, k := range []string{"first", "second"} {
		child := props[k].(map[string]any)
		cp, ok := child["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s did not expand: %v", k, child)
		}
		if _, ok := cp["x"]; !ok {
			t.Errorf("%s missing expanded field x: %v", k, cp)
		}
	}
}
