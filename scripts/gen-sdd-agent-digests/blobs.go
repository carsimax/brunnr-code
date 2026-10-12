package main

import "strconv"

func unquote(tag, literal string) string {
	value, err := strconv.Unquote(`"` + literal + `"`)
	if err != nil {
		fail("%s: unquote %q: %v", tag, literal, err)
	}
	return value
}

var blobs = map[string]string{}

func cachedBlob(blob string) string {
	if content, ok := blobs[blob]; ok {
		return content
	}
	content := git("cat-file", "blob", blob)
	blobs[blob] = content
	return content
}
