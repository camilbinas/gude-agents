package agent

import "encoding/json"

// jsonMessage and jsonContentBlock are the package-local JSON envelope for the
// sealed Message/ContentBlock union. They exist so a Snapshot blob can carry
// canonical transcript messages without the agent package importing a
// conversation backend (which would be an import cycle).
type jsonMessage struct {
	Role    string             `json:"role"`
	Content []jsonContentBlock `json:"content"`
}

type jsonContentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	ToolUse string          `json:"tool_use_id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	Content string          `json:"content,omitempty"`
	IsError bool            `json:"is_error,omitempty"`

	ImageData     []byte `json:"image_data,omitempty"`
	ImageBase64   string `json:"image_base64,omitempty"`
	ImageURL      string `json:"image_url,omitempty"`
	ImageMIMEType string `json:"image_mime_type,omitempty"`

	DocData          []byte `json:"doc_data,omitempty"`
	DocBase64        string `json:"doc_base64,omitempty"`
	DocURL           string `json:"doc_url,omitempty"`
	DocFileID        string `json:"doc_file_id,omitempty"`
	DocS3URI         string `json:"doc_s3_uri,omitempty"`
	DocS3BucketOwner string `json:"doc_s3_bucket_owner,omitempty"`
	DocMIMEType      string `json:"doc_mime_type,omitempty"`
	DocName          string `json:"doc_name,omitempty"`

	ToolResultImages []jsonImageSource `json:"tool_result_images,omitempty"`

	WidgetType    string          `json:"widget_type,omitempty"`
	WidgetPayload json.RawMessage `json:"widget_payload,omitempty"`
}

type jsonImageSource struct {
	Data     []byte `json:"data,omitempty"`
	Base64   string `json:"base64,omitempty"`
	URL      string `json:"url,omitempty"`
	MIMEType string `json:"mime_type,omitempty"`
}

func marshalMessages(messages []Message) []jsonMessage {
	out := make([]jsonMessage, len(messages))
	for i, msg := range messages {
		blocks := make([]jsonContentBlock, len(msg.Content))
		for j, cb := range msg.Content {
			blocks[j] = contentBlockToJSON(cb)
		}
		out[i] = jsonMessage{Role: string(msg.Role), Content: blocks}
	}
	return out
}

func unmarshalMessages(jmsgs []jsonMessage) ([]Message, error) {
	messages := make([]Message, len(jmsgs))
	for i, jm := range jmsgs {
		blocks := make([]ContentBlock, len(jm.Content))
		for j, jcb := range jm.Content {
			blocks[j] = jsonToContentBlock(jcb)
		}
		messages[i] = Message{Role: Role(jm.Role), Content: blocks}
	}
	return messages, nil
}

func contentBlockToJSON(cb ContentBlock) jsonContentBlock {
	switch b := cb.(type) {
	case TextBlock:
		return jsonContentBlock{Type: "text", Text: b.Text}
	case ToolUseBlock:
		return jsonContentBlock{Type: "tool_use", ToolUse: b.ToolUseID, Name: b.Name, Input: b.Input}
	case ToolResultBlock:
		jcb := jsonContentBlock{Type: "tool_result", ToolUse: b.ToolUseID, Content: b.Content, IsError: b.IsError}
		for _, img := range b.Images {
			jcb.ToolResultImages = append(jcb.ToolResultImages, jsonImageSource{
				Data: img.Source.Data, Base64: img.Source.Base64, URL: img.Source.URL, MIMEType: img.Source.MIMEType,
			})
		}
		return jcb
	case ImageBlock:
		return jsonContentBlock{
			Type: "image", ImageData: b.Source.Data, ImageBase64: b.Source.Base64,
			ImageURL: b.Source.URL, ImageMIMEType: b.Source.MIMEType,
		}
	case DocumentBlock:
		return jsonContentBlock{
			Type: "document", DocData: b.Source.Data, DocBase64: b.Source.Base64, DocURL: b.Source.URL,
			DocFileID: b.Source.FileID, DocS3URI: b.Source.S3URI, DocS3BucketOwner: b.Source.S3BucketOwner,
			DocMIMEType: b.Source.MIMEType, DocName: b.Source.Name,
		}
	case WidgetBlock:
		return jsonContentBlock{Type: "widget", WidgetType: b.Type, WidgetPayload: b.Payload}
	default:
		return jsonContentBlock{Type: "unknown"}
	}
}

func jsonToContentBlock(jcb jsonContentBlock) ContentBlock {
	switch jcb.Type {
	case "text":
		return TextBlock{Text: jcb.Text}
	case "tool_use":
		return ToolUseBlock{ToolUseID: jcb.ToolUse, Name: jcb.Name, Input: jcb.Input}
	case "tool_result":
		trb := ToolResultBlock{ToolUseID: jcb.ToolUse, Content: jcb.Content, IsError: jcb.IsError}
		for _, img := range jcb.ToolResultImages {
			trb.Images = append(trb.Images, ImageBlock{Source: ImageSource{
				Data: img.Data, Base64: img.Base64, URL: img.URL, MIMEType: img.MIMEType,
			}})
		}
		return trb
	case "image":
		return ImageBlock{Source: ImageSource{
			Data: jcb.ImageData, Base64: jcb.ImageBase64, URL: jcb.ImageURL, MIMEType: jcb.ImageMIMEType,
		}}
	case "document":
		return DocumentBlock{Source: DocumentSource{
			Data: jcb.DocData, Base64: jcb.DocBase64, URL: jcb.DocURL, FileID: jcb.DocFileID,
			S3URI: jcb.DocS3URI, S3BucketOwner: jcb.DocS3BucketOwner, MIMEType: jcb.DocMIMEType, Name: jcb.DocName,
		}}
	case "widget":
		return WidgetBlock{Type: jcb.WidgetType, Payload: jcb.WidgetPayload}
	default:
		return TextBlock{}
	}
}
