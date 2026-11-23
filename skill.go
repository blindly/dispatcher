package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const skillName = "dispatcher-configuration"

// skillRoots are agent skill directories to install into, each guarded by an
// anchor dir that must already exist — we never create agent config dirs on
// machines that don't use that agent.
var skillRoots = []struct{ anchor, dir string }{
	{".config/spec", ".config/spec/skills"},   // spec skill library (repo installs symlink here)
	{".claude", ".claude/skills"},             // Claude Code global skills
	{".config/devin", ".config/devin/skills"}, // Devin global skills
}

func skillTargets(home string) (install []string, refresh []string) {
	for _, r := range skillRoots {
		if _, err := os.Stat(filepath.Join(home, r.anchor)); err != nil {
			continue
		}
		target := filepath.Join(home, r.dir, skillName, "SKILL.md")
		install = append(install, target)
		if _, err := os.Stat(target); err == nil {
			refresh = append(refresh, target)
		}
	}
	return install, refresh
}

func writeSkill(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(skillMD), 0644)
}

func installSkill() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Cannot determine home directory: %v\n", err)
		os.Exit(1)
	}
	targets, _ := skillTargets(home)
	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "No agent skill directories found (~/.config/spec, ~/.claude, ~/.config/devin)")
		os.Exit(1)
	}
	failed := false
	specLib := ""
	for _, t := range targets {
		verb := "Installed"
		if _, err := os.Stat(t); err == nil {
			verb = "Updated"
		}
		if err := writeSkill(t); err != nil {
			fmt.Fprintf(os.Stderr, "skill: %s: %v\n", t, err)
			failed = true
			continue
		}
		fmt.Printf("%s %s\n", verb, t)
		if strings.Contains(t, filepath.Join("spec", "skills")) {
			specLib = t
		}
	}
	if specLib != "" {
		fmt.Printf("\nEnable in a spec repo with: spec skill use %s\n", skillName)
	}
	if failed {
		os.Exit(1)
	}
}

// refreshSkills overwrites already-installed SKILL.md copies after a
// self-update so installed skills track the binary version. It never
// creates new install locations.
func refreshSkills() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	_, refresh := skillTargets(home)
	for _, t := range refresh {
		if err := writeSkill(t); err == nil {
			fmt.Printf("Skill refreshed: %s\n", t)
		}
	}
}
