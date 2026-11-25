package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Job struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Time    int64  `json:"time"`
}

func ReadJob(path string) (Job, error) {
	var job Job
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &job)
	}
	return job, err
}

func WriteJob(path string, job Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".job-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), path)
}
