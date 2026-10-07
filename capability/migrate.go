// One-shot migration off the retired capability packs: packs that
// carried instruction fabric (a prompt and/or manual references)
// become skills under the same key; model-only or tool-only packs
// have no skill shape and are dropped. The old file is renamed .bak
// by the caller after the migration lands — this file only reads.
package capability

import (
	"encoding/json"
	"os"
)

// legacyPack mirrors the retired Pack's on-disk shape — only the
// slots a Skill can inherit are decoded; the model/tool slots are
// left to die with the format.
type legacyPack struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Desc      string   `json:"desc,omitempty"`
	Prompt    string   `json:"prompt,omitempty"`
	Manuals   []string `json:"manuals,omitempty"`
	CreatedBy string   `json:"created_by,omitempty"`
	CreatedTS int64    `json:"created_ts,omitempty"`
}

// MigrateLegacyPacks reads ~/.niuma/capabilities.json (path given,
// missing file = nothing to do) and folds every pack worth keeping
// into a Skill: same key, same name/desc, Body = the old prompt
// slot, manuals preserved. A pack with neither prompt nor manuals
// (model/tool-only) is not carried over. Provenance survives.
// Validation is the store's business at UpsertSkill time.
func MigrateLegacyPacks(path string) ([]Skill, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var packs []legacyPack
	if err := json.Unmarshal(b, &packs); err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(packs))
	for _, p := range packs {
		if p.Prompt == "" && len(p.Manuals) == 0 {
			continue
		}
		out = append(out, Skill{
			Key: p.Key, Name: p.Name, Desc: p.Desc,
			Body: p.Prompt, Manuals: p.Manuals,
			CreatedBy: p.CreatedBy, CreatedTS: p.CreatedTS,
		})
	}
	return out, nil
}
