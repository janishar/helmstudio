package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// videoInputs adds helm run's video flags to inputs. --duration sets the workflow's one frames param (a count, or
// "auto": the model picks the length and the run reports it). --image (frame 0) and each --keyframe <frame>:<take>
// become, in that order, the rows of its one keyframe param (a rows param whose item has an Image field and a
// `position: seconds` field) at seconds = frame / fps. A take that names a file is uploaded first.
func (w workflow) videoInputs(inputs map[string]any, image string, keyframes []string, duration string) error {
	if duration != "" {
		key, _, err := w.framesParam()
		if err != nil {
			return fmt.Errorf("--duration: %w", err)
		}
		v, err := w.value(key, duration)
		if err != nil {
			return fmt.Errorf("--duration %w", err)
		}
		inputs[key] = v
	}
	if image != "" {
		keyframes = append([]string{"0:" + image}, keyframes...)
	}
	if len(keyframes) == 0 {
		return nil
	}
	key, imageField, atField, err := w.keyframeParam()
	if err != nil {
		return err
	}
	rows := []any{}
	for _, kf := range keyframes {
		f, take, _ := strings.Cut(kf, ":")
		frame, err := strconv.Atoi(f)
		if err != nil || frame < 0 || take == "" {
			return fmt.Errorf("--keyframe %q: want <frame>:<take id or file>", kf)
		}
		seconds := 0.0
		if frame > 0 {
			fps, err := w.fps(inputs)
			if err != nil {
				return fmt.Errorf("--keyframe %q: %w", kf, err)
			}
			seconds = float64(frame) / fps
		}
		id, err := w.take(take)
		if err != nil {
			return err
		}
		rows = append(rows, map[string]any{imageField: map[string]any{"take_id": id}, atField: seconds})
	}
	inputs[key] = rows
	return nil
}

// take is a take id, or the id of the file it names once uploaded (@file, or a path that exists).
func (w workflow) take(s string) (string, error) {
	file, at := strings.CutPrefix(s, "@")
	if st, err := os.Stat(file); !at && (err != nil || st.IsDir()) {
		return s, nil
	}
	return w.upload(file)
}

// framesParam is the key and schema fps param name of the workflow's one frames param.
func (w workflow) framesParam() (key, fps string, err error) {
	for nid, n := range w.Nodes {
		for _, p := range w.NodeSchemas[n.Kind].Params {
			if p.Type == "frames" {
				if key != "" {
					return "", "", errors.New("the workflow has several frames params; use -p")
				}
				key, fps = nid+"."+p.Name, nid+"."+p.Fps
			}
		}
	}
	if key == "" {
		return "", "", fmt.Errorf("workflow %s has no length", w.ID)
	}
	return key, fps, nil
}

// fps is the frame rate the run will have: -p's value, else the workflow's, else the schema default.
func (w workflow) fps(inputs map[string]any) (float64, error) {
	_, key, err := w.framesParam()
	if err != nil {
		return 0, err
	}
	node, name, _ := strings.Cut(key, ".")
	v, ok := inputs[key]
	if !ok {
		v, ok = w.Nodes[node].Params[name]
	}
	for _, p := range w.NodeSchemas[w.Nodes[node].Kind].Params {
		if !ok && p.Name == name {
			v = p.Default
		}
	}
	switch n := v.(type) {
	case int64:
		return float64(n), nil
	case float64:
		if n > 0 {
			return n, nil
		}
	}
	return 0, fmt.Errorf("%s: no frame rate", key)
}

// keyframeParam is the key, Image field and seconds field of the workflow's one keyframe rows param.
func (w workflow) keyframeParam() (key, image, at string, err error) {
	for nid, n := range w.Nodes {
		for _, p := range w.NodeSchemas[n.Kind].Params {
			img, sec := "", ""
			for f, t := range p.Item {
				if t == "Image" {
					img = f
				} else if m, ok := t.(map[string]any); ok && m["position"] == "seconds" {
					sec = f
				}
			}
			if p.Type == "rows" && img != "" && sec != "" {
				if key != "" {
					return "", "", "", errors.New("--image/--keyframe: the workflow has several keyframe inputs; use -p")
				}
				key, image, at = nid+"."+p.Name, img, sec
			}
		}
	}
	if key == "" {
		return "", "", "", fmt.Errorf("--image/--keyframe: workflow %s takes no keyframes", w.ID)
	}
	return key, image, at, nil
}

// mediaInputs adds helm run's --media (node.param=<take>[:in:out], each a media param) and --span (start:end, the
// workflow's one span param) to inputs.
func (w workflow) mediaInputs(inputs map[string]any, medias []string, spanVal string) error {
	for _, m := range medias {
		key, val, ok := strings.Cut(m, "=")
		if !ok || w.paramType(key) != "media" {
			return fmt.Errorf("--media %q: want <node.param>=<take id>[:in:out] on a media input", m)
		}
		v, err := media(key, val)
		if err != nil {
			return fmt.Errorf("--media %w", err)
		}
		inputs[key] = v
	}
	if spanVal == "" {
		return nil
	}
	key := ""
	for nid, n := range w.Nodes {
		for _, p := range w.NodeSchemas[n.Kind].Params {
			if p.Type == "span" {
				if key != "" {
					return errors.New("--span: the workflow has several ranges; use -p")
				}
				key = nid + "." + p.Name
			}
		}
	}
	if key == "" {
		return fmt.Errorf("--span: workflow %s takes no range", w.ID)
	}
	v, err := span(key, spanVal)
	if err != nil {
		return fmt.Errorf("--span %w", err)
	}
	inputs[key] = v
	return nil
}

// paramType is the schema type of node.param ("" when the workflow has no such param).
func (w workflow) paramType(key string) string {
	node, name, _ := strings.Cut(key, ".")
	for _, p := range w.NodeSchemas[w.Nodes[node].Kind].Params {
		if p.Name == name {
			return p.Type
		}
	}
	return ""
}

// media is a MediaRef: <take id>[:in:out].
func media(key, val string) (any, error) {
	parts := strings.Split(val, ":")
	if parts[0] == "" || (len(parts) != 1 && len(parts) != 3) {
		return nil, fmt.Errorf("%s: want <take id>[:in:out]", key)
	}
	ref := map[string]any{"take_id": parts[0]}
	if len(parts) == 3 {
		for i, name := range []string{"in", "out"} {
			f, err := strconv.ParseFloat(parts[i+1], 64)
			if err != nil {
				return nil, fmt.Errorf("%s: %s is not a number of seconds", key, name)
			}
			ref[name] = f
		}
	}
	return ref, nil
}

// span is a SpanValue: start:end.
func span(key, val string) (any, error) {
	a, b, ok := strings.Cut(val, ":")
	start, err1 := strconv.ParseFloat(a, 64)
	end, err2 := strconv.ParseFloat(b, 64)
	if !ok || err1 != nil || err2 != nil {
		return nil, fmt.Errorf("%s: want start:end in seconds", key)
	}
	return map[string]any{"start": start, "end": end}, nil
}
