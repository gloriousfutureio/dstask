package dstask

// this file represents the interface to the state specific to the PC dstask is
// on is stored. This is very minimal at the moment -- just the current
// context. It will probably remain that way.

import (
	"encoding/gob"
	"os"
	"path/filepath"
)

type State struct {
	// context to automatically apply to all queries and new tasks
	context CmdLine
}

func (state State) Save() {
	fp := MustExpandHome(STATE_FILE)
	os.MkdirAll(filepath.Dir(fp), os.ModePerm)
	MustWriteGob(fp, &state)
}

func LoadState() State {
	fp := MustExpandHome(STATE_FILE)
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		return State{}
	}

	state := State{}
	MustReadGob(fp, &state)
	return state
}

func (state State) GetContext() CmdLine {
	return state.context
}

func (state *State) SetContext(context CmdLine) {
	if len(context.IDs) != 0 {
		ExitFail("Context cannot contain IDs")
	}

	if context.Text != "" {
		ExitFail("Context cannot contain text")
	}

	state.context = context
}

func (state *State) ClearContext() {
	state.SetContext(CmdLine{})
}

func MustWriteGob(filePath string, object interface{}) {
	file, err := os.Create(filePath)
	defer file.Close()

	if err != nil {
		ExitFail("Failed to open %s for writing: ", filePath)
	}

	encoder := gob.NewEncoder(file)
	encoder.Encode(object)
}

func MustReadGob(filePath string, object interface{}) {
	file, err := os.Open(filePath)
	defer file.Close()

	if err != nil {
		ExitFail("Failed to open %s for reading: ", filePath)
	}

	decoder := gob.NewDecoder(file)
	err = decoder.Decode(object)

	if err != nil {
		ExitFail("Failed to parse gob: %s", filePath)
	}
}
