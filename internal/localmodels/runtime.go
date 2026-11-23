package localmodels

import _ "embed"

// Runtime is how a family of models runs: the Python it needs, the packages
// installed into its environment, and the worker script that serves requests.
// Models sharing a runtime share one environment under local/runtimes/<name>.
type Runtime struct {
	Name     string
	Python   string   // version uv installs, e.g. "3.12"
	Packages []string // pip requirements, including the Hugging Face downloader
	Worker   []byte   // the worker script, written next to the environment at start
}

// The model's own runtime/ folder holds the julia2 package; the worker adds it
// to sys.path, so only its dependencies are installed here.
//
//go:embed julia2_worker.py
var julia2Worker []byte

var julia2Runtime = &Runtime{
	Name:     "julia2",
	Python:   "3.12",
	Packages: []string{"torch>=2.6", "safetensors", "tokenizers", "numpy", "pillow", "soundfile", "scipy", "huggingface_hub"},
	Worker:   julia2Worker,
}
