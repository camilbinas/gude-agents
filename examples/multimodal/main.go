// Run with a local file: go run ./multimodal -image ./photo.jpg
// Or a remote image:       go run ./multimodal -image-url https://example.com/photo -image-mime image/jpeg
// A document works too:    go run ./multimodal -document ./report.pdf
// Or a remote document:    go run ./multimodal -document-url https://example.com/report -document-mime application/pdf
//
// Demonstrates both source forms: bytes in Source.Data and a provider-fetched
// Source.URL. Use one attachment option at a time.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/camilbinas/gude-agents/agent"
	"github.com/camilbinas/gude-agents/agent/provider/bedrock"
)

func main() {
	imagePath := flag.String("image", "", "local image path")
	imageURL := flag.String("image-url", "", "remote image URL")
	imageMIME := flag.String("image-mime", "", "MIME type required with -image-url, for example image/jpeg")
	documentPath := flag.String("document", "", "local document path")
	documentURL := flag.String("document-url", "", "remote document URL")
	documentMIME := flag.String("document-mime", "", "MIME type required with -document-url, for example application/pdf")
	flag.Parse()
	ctx := agent.Background()
	var attachments *agent.Context
	switch {
	case *imagePath != "":
		data, err := os.ReadFile(*imagePath)
		if err != nil {
			log.Fatal(err)
		}
		mime, err := agent.ImageMIMEFromExt(filepath.Ext(*imagePath))
		if err != nil {
			log.Fatal(err)
		}
		attachments = ctx.WithImages([]agent.ImageBlock{{Source: agent.ImageSource{Data: data, MIMEType: mime}}})
	case *imageURL != "":
		if *imageMIME == "" {
			log.Fatal("-image-mime is required with -image-url; do not guess remote content types")
		}
		attachments = ctx.WithImages([]agent.ImageBlock{{Source: agent.ImageSource{URL: *imageURL, MIMEType: *imageMIME}}})
	case *documentPath != "":
		data, err := os.ReadFile(*documentPath)
		if err != nil {
			log.Fatal(err)
		}
		mime, err := agent.DocumentMIMEFromExt(filepath.Ext(*documentPath))
		if err != nil {
			log.Fatal(err)
		}
		attachments = ctx.WithDocuments([]agent.DocumentBlock{{Source: agent.DocumentSource{Data: data, MIMEType: mime, Name: filepath.Base(*documentPath)}}})
	case *documentURL != "":
		if *documentMIME == "" {
			log.Fatal("-document-mime is required with -document-url; do not guess remote content types")
		}
		attachments = ctx.WithDocuments([]agent.DocumentBlock{{Source: agent.DocumentSource{URL: *documentURL, MIMEType: *documentMIME, Name: "remote-document"}}})
	default:
		log.Fatal("provide -image, -image-url, -document, or -document-url")
	}
	a, err := agent.New(bedrock.Must(bedrock.Standard()), "Analyze the supplied image or document and answer concisely.")
	if err != nil {
		log.Fatal(err)
	}
	result, err := a.Invoke(attachments, "What are the important details in this attachment?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Text)
}
