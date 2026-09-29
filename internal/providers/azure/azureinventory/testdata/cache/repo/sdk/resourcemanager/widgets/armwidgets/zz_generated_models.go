package armwidgets

type Encryption struct {
	KeyURL      *string      `json:"keyUrl,omitempty"`
	SourceVault *SubResource `json:"sourceVault,omitempty"`
}
