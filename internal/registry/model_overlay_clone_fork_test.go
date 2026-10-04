package registry

import (
	"reflect"
	"slices"
	"testing"
)

func TestCloneStaticModelsCatalogPreservesDevinAndMeta(t *testing.T) {
	base := testModelsCatalog()
	base.Devin = []*ModelInfo{{
		ID:                  "devin-original",
		DisplayName:         "Devin original",
		SupportedParameters: []string{"temperature"},
		Thinking:            &ThinkingSupport{Levels: []string{"high"}},
	}}
	base.Meta = []*ModelInfo{{
		ID:                  "meta-original",
		DisplayName:         "Meta original",
		SupportedParameters: []string{"top_p"},
		Thinking:            &ThinkingSupport{Levels: []string{"medium"}},
	}}

	cloned := cloneStaticModelsCatalog(&base)
	if !reflect.DeepEqual(cloned, &base) {
		t.Fatalf("cloned catalog lost sections: Devin = %+v, Meta = %+v", cloned.Devin, cloned.Meta)
	}
	for _, section := range []struct {
		name     string
		original []*ModelInfo
		cloned   []*ModelInfo
	}{
		{name: "devin", original: base.Devin, cloned: cloned.Devin},
		{name: "meta", original: base.Meta, cloned: cloned.Meta},
	} {
		original := section.original[0]
		copyModel := section.cloned[0]
		if original == copyModel || original.Thinking == copyModel.Thinking {
			t.Fatalf("%s model or thinking metadata was not deep-cloned", section.name)
		}
		originalName := original.DisplayName
		originalParameter := original.SupportedParameters[0]
		originalLevel := original.Thinking.Levels[0]
		copyModel.DisplayName = "changed"
		copyModel.SupportedParameters[0] = "changed"
		copyModel.Thinking.Levels[0] = "changed"
		if original.DisplayName != originalName || original.SupportedParameters[0] != originalParameter || original.Thinking.Levels[0] != originalLevel {
			t.Fatalf("mutating cloned %s metadata changed the base catalog", section.name)
		}
		section.cloned[0] = nil
		if section.original[0] == nil {
			t.Fatalf("%s model slice was not cloned", section.name)
		}
	}
}

func TestPublishCatalogWithLocalOverlayPreservesUpdatedDevinAndMeta(t *testing.T) {
	restoreModelsCatalogForTest(t)
	t.Setenv(localModelsOverlayEnv, writeModelsOverlayFile(t, staticModelsJSON{
		Gemini: []*ModelInfo{{ID: "gemini-local-overlay"}},
	}))

	initial := testModelsCatalog()
	initial.Devin = []*ModelInfo{{ID: "devin-old"}}
	initial.Meta = []*ModelInfo{{ID: "meta-old"}}
	if err := loadModelsFromBytes(mustMarshalCatalog(t, initial), "initial"); err != nil {
		t.Fatalf("load initial catalog: %v", err)
	}
	if got := getModels(); !reflect.DeepEqual(got.Devin, initial.Devin) || !reflect.DeepEqual(got.Meta, initial.Meta) {
		t.Fatalf("initial overlay dropped unrelated sections: Devin = %+v, Meta = %+v", got.Devin, got.Meta)
	}

	updated := testModelsCatalog()
	updated.Devin = []*ModelInfo{{ID: "devin-new", Thinking: &ThinkingSupport{Levels: []string{"high"}}}}
	updated.Meta = []*ModelInfo{{ID: "meta-new", SupportedParameters: []string{"temperature"}}}
	changed, err := publishCatalogBytes(mustMarshalCatalog(t, updated))
	if err != nil {
		t.Fatalf("publish updated catalog: %v", err)
	}
	got := getModels()
	if lookupModelByID(got.Gemini, "gemini-local-overlay") == nil {
		t.Fatal("published catalog lost the local Gemini overlay")
	}
	if !reflect.DeepEqual(got.Devin, updated.Devin) || !reflect.DeepEqual(got.Meta, updated.Meta) {
		t.Fatalf("overlay discarded updated unrelated sections: Devin = %+v, Meta = %+v", got.Devin, got.Meta)
	}
	if !slices.Contains(changed, "devin") || !slices.Contains(changed, "meta") {
		t.Fatalf("updated unrelated sections were not detected: %v", changed)
	}
}
