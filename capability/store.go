// Store: the global skill & MCP library, persisted agents.Store
// style (v2 P1 dialect): one mutex, atomic full-file rewrite
// (temp+rename), empty path = in-memory only, corrupt file surfaces
// as an Open error. Two files: skills.json (an object carrying the
// model-authoring switch + the list) and mcps.json (a bare array).
package capability

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/WWestC/Niuma_Studio/i18n"
	"github.com/WWestC/Niuma_Studio/persist"
	"github.com/WWestC/Niuma_Studio/util"
)

// skillsFile is the on-disk shape of skills.json: the library plus
// the studio-wide model-authoring switch riding in the same file
// (the switch governs this library, so it lives with it).
type skillsFile struct {
	ModelSkills bool    `json:"model_skills"`
	Skills      []Skill `json:"skills"`
}

// Store is the persisted skill + MCP library. All methods are safe
// for concurrent use.
type Store struct {
	mu          sync.Mutex
	skillsPath  string
	mcpsPath    string
	skills      []Skill
	mcps        []MCPServer
	modelSkills bool // the 自制技能 switch
	rev         uint64
}

// DefaultPaths returns the conventional locations:
// ~/.niuma/skills.json and ~/.niuma/mcps.json.
func DefaultPaths() (skills, mcps string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(home, ".niuma")
	return filepath.Join(dir, "skills.json"), filepath.Join(dir, "mcps.json"), nil
}

// Open loads the store from its two files. A missing file starts
// empty on that side; a corrupt file is an error and how to degrade
// is the caller's call. An empty path for either side keeps that side
// in-memory only.
func Open(skillsPath, mcpsPath string) (*Store, error) {
	s := &Store{skillsPath: skillsPath, mcpsPath: mcpsPath}
	if skillsPath != "" {
		b, err := os.ReadFile(skillsPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			var f skillsFile
			if err := json.Unmarshal(b, &f); err != nil {
				return nil, fmt.Errorf("parse %s: %w", skillsPath, err)
			}
			s.skills = f.Skills
			s.modelSkills = f.ModelSkills
		}
	}
	if mcpsPath != "" {
		b, err := os.ReadFile(mcpsPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if err == nil {
			if err := json.Unmarshal(b, &s.mcps); err != nil {
				return nil, fmt.Errorf("parse %s: %w", mcpsPath, err)
			}
		}
	}
	return s, nil
}

// UpsertSkill stores the skill under an exact key: an existing key is
// REPLACED wholesale (clearing a field is expressible), a new key is
// appended — never a twin. Creation provenance is stamped on create
// and survives updates; the key itself is immutable identity.
func (s *Store) UpsertSkill(sk Skill) (Skill, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sk = sk.sanitize()
	if err := sk.Validate(); err != nil {
		return Skill{}, err
	}
	if i := s.findIndexSkillLocked(sk.Key); i >= 0 {
		sk.CreatedBy = s.skills[i].CreatedBy
		sk.CreatedTS = s.skills[i].CreatedTS
		s.skills[i] = sk
	} else {
		if len(s.skills) >= MaxSkills {
			return Skill{}, errors.New(i18n.Sf("技能数已达上限 %d 个", MaxSkills))
		}
		sk.CreatedTS = util.Now()
		s.skills = append(s.skills, sk)
	}
	s.rev++
	s.saveLocked()
	return sk, nil
}

// RemoveSkill deletes the skill with that exact key and persists;
// reports whether it existed. Assemblies referencing the key go dry
// (Compose never sees the skill).
func (s *Store) RemoveSkill(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sk := range s.skills {
		if sk.Key == key {
			s.skills = append(s.skills[:i], s.skills[i+1:]...)
			s.rev++
			s.saveLocked()
			return true
		}
	}
	return false
}

// GetSkill returns one skill by exact key.
func (s *Store) GetSkill(key string) (Skill, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.findIndexSkillLocked(key); i >= 0 {
		return s.skills[i], true
	}
	return Skill{}, false
}

// ListSkills returns a copy of the skills in insertion order.
func (s *Store) ListSkills() []Skill {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Skill(nil), s.skills...)
}

// UpsertMCP stores the MCP server under an exact key, same
// whole-replace/upsert discipline as skills.
func (s *Store) UpsertMCP(m MCPServer) (MCPServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m = m.sanitize()
	if err := m.Validate(); err != nil {
		return MCPServer{}, err
	}
	if i := s.findIndexMCPLocked(m.Key); i >= 0 {
		m.CreatedBy = s.mcps[i].CreatedBy
		m.CreatedTS = s.mcps[i].CreatedTS
		s.mcps[i] = m
	} else {
		if len(s.mcps) >= MaxMCPs {
			return MCPServer{}, errors.New(i18n.Sf("MCP 服务数已达上限 %d 个", MaxMCPs))
		}
		m.CreatedTS = util.Now()
		s.mcps = append(s.mcps, m)
	}
	s.rev++
	s.saveLocked()
	return m, nil
}

// RemoveMCP deletes the MCP server with that exact key and persists;
// reports whether it existed.
func (s *Store) RemoveMCP(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.mcps {
		if m.Key == key {
			s.mcps = append(s.mcps[:i], s.mcps[i+1:]...)
			s.rev++
			s.saveLocked()
			return true
		}
	}
	return false
}

// GetMCP returns one MCP server by exact key.
func (s *Store) GetMCP(key string) (MCPServer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.findIndexMCPLocked(key); i >= 0 {
		return s.mcps[i], true
	}
	return MCPServer{}, false
}

// ListMCPs returns a copy of the MCP servers in insertion order.
func (s *Store) ListMCPs() []MCPServer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]MCPServer(nil), s.mcps...)
}

// ModelAuthoring reads the studio-wide 自制技能 switch.
func (s *Store) ModelAuthoring() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modelSkills
}

// SetModelAuthoring flips the studio-wide 自制技能 switch and
// persists it with the library.
func (s *Store) SetModelAuthoring(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.modelSkills == on {
		return
	}
	s.modelSkills = on
	s.rev++
	s.saveLocked()
}

// Rev returns the mutation counter; callers use it to detect changes
// without comparing bodies.
func (s *Store) Rev() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rev
}

func (s *Store) findIndexSkillLocked(key string) int {
	for i, sk := range s.skills {
		if sk.Key == key {
			return i
		}
	}
	return -1
}

func (s *Store) findIndexMCPLocked(key string) int {
	for i, m := range s.mcps {
		if m.Key == key {
			return i
		}
	}
	return -1
}

// saveLocked writes both JSON snapshots atomically (persist.Save —
// temp file beside the target, fsync, then rename), creating the
// parent directory on first save. Persistence failures are logged but
// do not abort the in-memory mutation.
func (s *Store) saveLocked() {
	if s.skillsPath != "" {
		b, err := json.MarshalIndent(skillsFile{ModelSkills: s.modelSkills, Skills: s.skills}, "", "  ")
		if err == nil {
			err = atomicWrite(s.skillsPath, b)
		}
		if err != nil {
			log.Printf("save skills file: %v", err)
		}
	}
	if s.mcpsPath != "" {
		b, err := json.MarshalIndent(s.mcps, "", "  ")
		if err == nil {
			err = atomicWrite(s.mcpsPath, b)
		}
		if err != nil {
			log.Printf("save mcps file: %v", err)
		}
	}
}

func atomicWrite(path string, b []byte) error {
	// 0600（安全核查修复）：这份文件里有 MCP 的明文 Authorization 头——
	// 应用自己落盘的唯一真密钥，与其余 0600 的凭证文件同规格，不再 0644。
	return persist.Save(path, b, 0o600)
}
