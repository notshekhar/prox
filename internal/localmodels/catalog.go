// Package localmodels runs models on this machine instead of an account: a
// catalog of what can be installed, the installer that puts a model and its
// runtime under ~/.prox/local, and the worker processes that serve them.
// No sign-in is involved; a local provider is ready once its model is installed.
package localmodels

import "strings"

// Kind says which API a model answers. Only decision models exist today; a
// chat runtime (an Ollama-style LLM) would add its own kind.
type Kind string

const KindDecision Kind = "decision"

// Model is one installable local model.
type Model struct {
	ID           string // what requests name, e.g. "julia-2-flash"
	Provider     string // provider id, e.g. "supersonic"
	ProviderName string // shown to people, e.g. "Supersonic Labs"
	Description  string
	Kind         Kind
	Repo         string   // Hugging Face repository the weights come from
	Revision     string   // the repository commit prox was checked against
	Exclude      []string // Python runtimes: repository files left out of the download
	Files        []string // native runtimes: the only files downloaded
	Required     []string // files a complete install has; older installs lacking one need installing again
	Inputs       []MediaKind
	DownloadSize string // rough size of the download, for the install prompt
	ReleaseDate  string // when the repository was published
	Runtime      Runtime
}

// QualifiedID is the provider-prefixed name, e.g. "supersonic/julia-2-flash".
func (m Model) QualifiedID() string { return m.Provider + "/" + m.ID }

var catalog = []Model{
	{
		ID:           "julia-2-flash",
		Provider:     "supersonic",
		ProviderName: "Supersonic Labs",
		Description:  "Julia 2 Flash: answers choice, score and yes/no questions about text, JSON, images and speech with probabilities. Nothing is generated.",
		Kind:         KindDecision,
		Repo:         "SupersonicLabs/Julia-2-Flash",
		Revision:     "2656be556d8a36be317ce7cc27cb6d0fcdbef67b",
		Exclude:      []string{"assets/*"}, // the model card's pictures
		Required:     []string{"text.safetensors", "image.safetensors", "audio.safetensors"},
		Inputs:       []MediaKind{MediaImage, MediaAudio},
		DownloadSize: "660 MB",
		ReleaseDate:  "2026-11-17",
		Runtime:      julia2Runtime,
	},
	{
		ID:           "julia-2-nano",
		Provider:     "supersonic",
		ProviderName: "Supersonic Labs",
		Description:  "Julia 2 Nano: the microcontroller Julia, run inside prox with no Python. Text in English and Portuguese (agrees with Julia 2 about 6 times in 10), plus photos and spoken banking requests scored against fixed label lists.",
		Kind:         KindDecision,
		Repo:         "SupersonicLabs/Julia-2-Nano",
		Revision:     "96bfb8a5cbc51643bb28fb7b96f45feed8bb0449",
		Files:        []string{nanoTextWeights, nanoImageWeights, nanoSpeechWeights, nanoImageLabels, nanoSpeechLabels, "LICENSE", "NOTICE"},
		Required:     []string{nanoTextWeights, nanoImageWeights, nanoSpeechWeights, nanoImageLabels, nanoSpeechLabels},
		Inputs:       []MediaKind{MediaImage, MediaAudio},
		DownloadSize: "15 MB",
		ReleaseDate:  "2026-11-21",
		Runtime:      nanoTextRuntime,
	},
}

// Catalog lists every model prox knows how to install.
func Catalog() []Model { return append([]Model(nil), catalog...) }

// Find looks a model up by its id or provider-prefixed id, ignoring case.
func Find(name string) (Model, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, m := range catalog {
		if name == m.ID || name == m.QualifiedID() {
			return m, true
		}
	}
	return Model{}, false
}
