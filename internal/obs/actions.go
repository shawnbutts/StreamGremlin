package obs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Every action needs a live session and fails at once without one: nothing
// is queued for later. Each sets an absolute state where OBS allows it, so
// repeating one after ErrOutcomeUnknown is safe; "toggle" is not, and says so.

// Scenes lists OBS's scenes, in OBS's order, and the current one.
func (s *Service) Scenes(ctx context.Context) (current string, scenes []string, err error) {
	c, err := s.live()
	if err != nil {
		return "", nil, err
	}
	var out struct {
		Current string `json:"currentProgramSceneName"`
		Scenes  []struct {
			Name  string `json:"sceneName"`
			Index int    `json:"sceneIndex"`
		} `json:"scenes"`
	}
	if err := c.request(ctx, "GetSceneList", nil, &out); err != nil {
		return "", nil, err
	}
	// Index 0 is the bottom of OBS's list; show them top first, as OBS does.
	sort.SliceStable(out.Scenes, func(i, j int) bool { return out.Scenes[i].Index > out.Scenes[j].Index })
	for _, sc := range out.Scenes {
		scenes = append(scenes, sc.Name)
	}
	return out.Current, scenes, nil
}

// SwitchScene makes scene the program scene. by names who asked, for
// ownership ("app", "workflow:<id>", "snapshot:<id>").
func (s *Service) SwitchScene(ctx context.Context, scene, by string) (previous string, err error) {
	if strings.TrimSpace(scene) == "" {
		return "", fmt.Errorf("%w: which scene?", ErrInvalid)
	}
	c, err := s.live()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	previous = s.ctl.scene
	// Recorded before asking: OBS may announce the change before it answers.
	s.ctl.pending = append(s.ctl.pending, pendingSwitch{scene: scene, by: by, at: s.now()})
	s.mu.Unlock()
	if err := c.request(ctx, "SetCurrentProgramScene", map[string]any{"sceneName": scene}, nil); err != nil {
		if !isUnknown(err) {
			s.dropPending(scene, by)
		}
		return previous, err
	}
	return previous, nil
}

func isUnknown(err error) bool { return errors.Is(err, ErrOutcomeUnknown) }

func (s *Service) dropPending(scene, by string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.ctl.pending {
		if p.scene == scene && p.by == by {
			s.ctl.pending = append(s.ctl.pending[:i], s.ctl.pending[i+1:]...)
			return
		}
	}
}

// SceneItem is one source placed in a scene.
type SceneItem struct {
	ID      int    `json:"id"`
	Source  string `json:"source"`
	Visible bool   `json:"visible"`
	IsGroup bool   `json:"is_group"`
}

// SceneItems lists a scene's sources, top first, as OBS shows them.
func (s *Service) SceneItems(ctx context.Context, scene string) ([]SceneItem, error) {
	c, err := s.live()
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []struct {
			ID      int    `json:"sceneItemId"`
			Source  string `json:"sourceName"`
			Enabled bool   `json:"sceneItemEnabled"`
			IsGroup *bool  `json:"isGroup"`
			Index   int    `json:"sceneItemIndex"`
		} `json:"sceneItems"`
	}
	if err := c.request(ctx, "GetSceneItemList", map[string]any{"sceneName": scene}, &out); err != nil {
		return nil, err
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].Index > out.Items[j].Index })
	items := make([]SceneItem, 0, len(out.Items))
	for _, it := range out.Items {
		items = append(items, SceneItem{ID: it.ID, Source: it.Source, Visible: it.Enabled, IsGroup: it.IsGroup != nil && *it.IsGroup})
	}
	return items, nil
}

// SetSourceVisible shows or hides source in scene (its first placement).
func (s *Service) SetSourceVisible(ctx context.Context, scene, source string, visible bool) (previous bool, err error) {
	c, err := s.live()
	if err != nil {
		return false, err
	}
	var id struct {
		ID int `json:"sceneItemId"`
	}
	if err := c.request(ctx, "GetSceneItemId", map[string]any{"sceneName": scene, "sourceName": source}, &id); err != nil {
		return false, err
	}
	var cur struct {
		Enabled bool `json:"sceneItemEnabled"`
	}
	if err := c.request(ctx, "GetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": id.ID}, &cur); err != nil {
		return false, err
	}
	err = c.request(ctx, "SetSceneItemEnabled", map[string]any{"sceneName": scene, "sceneItemId": id.ID, "sceneItemEnabled": visible}, nil)
	return cur.Enabled, err
}

// Mode is on, off or toggle. Toggle isn't safe to repeat, and two at once
// can cancel each other out.
type Mode string

const (
	ModeOn     Mode = "on"
	ModeOff    Mode = "off"
	ModeToggle Mode = "toggle"
)

func (m Mode) valid() bool { return m == ModeOn || m == ModeOff || m == ModeToggle }

// Filter is one filter on a source or scene.
type Filter struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
}

// Filters lists source's filters. A scene's name works too.
func (s *Service) Filters(ctx context.Context, source string) ([]Filter, error) {
	c, err := s.live()
	if err != nil {
		return nil, err
	}
	var out struct {
		Filters []struct {
			Name    string `json:"filterName"`
			Kind    string `json:"filterKind"`
			Enabled bool   `json:"filterEnabled"`
		} `json:"filters"`
	}
	if err := c.request(ctx, "GetSourceFilterList", map[string]any{"sourceName": source}, &out); err != nil {
		return nil, err
	}
	fs := make([]Filter, len(out.Filters))
	for i, f := range out.Filters {
		fs[i] = Filter{Name: f.Name, Kind: f.Kind, Enabled: f.Enabled}
	}
	return fs, nil
}

// SetFilter turns source's filter on or off, or flips it. It returns the
// new state.
func (s *Service) SetFilter(ctx context.Context, source, filter string, mode Mode) (bool, error) {
	if !mode.valid() {
		return false, fmt.Errorf("%w: on, off or toggle", ErrInvalid)
	}
	c, err := s.live()
	if err != nil {
		return false, err
	}
	want := mode == ModeOn
	if mode == ModeToggle {
		var cur struct {
			Enabled bool `json:"filterEnabled"`
		}
		if err := c.request(ctx, "GetSourceFilter", map[string]any{"sourceName": source, "filterName": filter}, &cur); err != nil {
			return false, err
		}
		want = !cur.Enabled
	}
	err = c.request(ctx, "SetSourceFilterEnabled", map[string]any{"sourceName": source, "filterName": filter, "filterEnabled": want}, nil)
	return want, err
}

// Input is an audio or video input (a source not tied to one scene).
type Input struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Inputs lists OBS's inputs, including global audio such as Mic/Aux.
func (s *Service) Inputs(ctx context.Context) ([]Input, error) {
	c, err := s.live()
	if err != nil {
		return nil, err
	}
	var out struct {
		Inputs []struct {
			Name string `json:"inputName"`
			Kind string `json:"inputKind"`
		} `json:"inputs"`
	}
	if err := c.request(ctx, "GetInputList", nil, &out); err != nil {
		return nil, err
	}
	in := make([]Input, len(out.Inputs))
	for i, x := range out.Inputs {
		in[i] = Input{Name: x.Name, Kind: x.Kind}
	}
	return in, nil
}

// SetMuted mutes, unmutes or flips an input, and returns whether it's muted.
func (s *Service) SetMuted(ctx context.Context, input string, mode Mode) (bool, error) {
	if !mode.valid() {
		return false, fmt.Errorf("%w: on, off or toggle", ErrInvalid)
	}
	c, err := s.live()
	if err != nil {
		return false, err
	}
	if mode == ModeToggle {
		var out struct {
			Muted bool `json:"inputMuted"`
		}
		err := c.request(ctx, "ToggleInputMute", map[string]any{"inputName": input}, &out)
		return out.Muted, err
	}
	err = c.request(ctx, "SetInputMute", map[string]any{"inputName": input, "inputMuted": mode == ModeOn}, nil)
	return mode == ModeOn, err
}

// MaxText caps a text source's new text.
const MaxText = 16 << 10

// SetText sets a text source's text, and nothing else. It refuses any other
// kind of source, so it never writes a stray setting into one.
func (s *Service) SetText(ctx context.Context, input, text string) error {
	if len(text) > MaxText {
		return fmt.Errorf("%w: text is over 16 KiB", ErrInvalid)
	}
	c, err := s.live()
	if err != nil {
		return err
	}
	var cur struct {
		Kind string `json:"inputKind"`
	}
	if err := c.request(ctx, "GetInputSettings", map[string]any{"inputName": input}, &cur); err != nil {
		return err
	}
	if !strings.HasPrefix(cur.Kind, "text_gdiplus") && !strings.HasPrefix(cur.Kind, "text_ft2_source") {
		return fmt.Errorf("%w: %q is a %s source, not a text source", ErrInvalid, input, cur.Kind)
	}
	return c.request(ctx, "SetInputSettings", map[string]any{"inputName": input, "inputSettings": map[string]any{"text": text}, "overlay": true}, nil)
}

// MediaActions are what a media source can be told to do.
var MediaActions = map[string]string{
	"play": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PLAY", "pause": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PAUSE",
	"stop": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_STOP", "restart": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_RESTART",
	"next": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_NEXT", "previous": "OBS_WEBSOCKET_MEDIA_INPUT_ACTION_PREVIOUS",
}

// Media plays, pauses, stops, restarts or skips a media source.
func (s *Service) Media(ctx context.Context, input, action string) error {
	a, ok := MediaActions[action]
	if !ok {
		return fmt.Errorf("%w: play, pause, stop, restart, next or previous", ErrInvalid)
	}
	c, err := s.live()
	if err != nil {
		return err
	}
	return c.request(ctx, "TriggerMediaInputAction", map[string]any{"inputName": input, "mediaAction": a}, nil)
}

// Recording starts or stops recording. It reads the state first and does
// nothing when it already is, so it's safe to repeat. Stopping returns the file.
func (s *Service) Recording(ctx context.Context, start bool) (changed bool, path string, err error) {
	c, err := s.live()
	if err != nil {
		return false, "", err
	}
	var st struct {
		Active bool `json:"outputActive"`
	}
	if err := c.request(ctx, "GetRecordStatus", nil, &st); err != nil {
		return false, "", err
	}
	if st.Active == start {
		return false, "", nil
	}
	if start {
		err = c.request(ctx, "StartRecord", nil, nil)
		if isCode(err, codeOutputRunning) {
			return false, "", nil // started meanwhile
		}
		return err == nil, "", err
	}
	var out struct {
		Path string `json:"outputPath"`
	}
	err = c.request(ctx, "StopRecord", nil, &out)
	if isCode(err, codeOutputNotRunning) {
		return false, "", nil
	}
	return err == nil, out.Path, err
}

// SaveReplay saves the replay buffer. The file arrives as broadcast.replay.saved.
func (s *Service) SaveReplay(ctx context.Context) error {
	c, err := s.live()
	if err != nil {
		return err
	}
	var st struct {
		Active bool `json:"outputActive"`
	}
	if err := c.request(ctx, "GetReplayBufferStatus", nil, &st); err != nil {
		return err
	}
	if !st.Active {
		return fmt.Errorf("obs: the replay buffer isn't running; start it in OBS first")
	}
	return c.request(ctx, "SaveReplayBuffer", nil, nil)
}

var (
	unsafeName  = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
	windowsPath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)
)

// Screenshot saves a PNG of source into folder, a full path on the computer
// OBS runs on. StreamGremlin names the file from the source and the time,
// so a screenshot never replaces a file and chat never picks where it goes.
func (s *Service) Screenshot(ctx context.Context, source, folder string) (string, error) {
	if !strings.HasPrefix(folder, "/") && !strings.HasPrefix(folder, `\\`) && !windowsPath.MatchString(folder) {
		return "", fmt.Errorf("%w: the folder must be a full path, like C:\\Screenshots or /Users/you/Screenshots", ErrInvalid)
	}
	c, err := s.live()
	if err != nil {
		return "", err
	}
	name := strings.Trim(unsafeName.ReplaceAllString(source, "-"), "-") + "-" + s.now().UTC().Format("20060102-150405.000") + ".png"
	sep := "/"
	if strings.Contains(folder, `\`) {
		sep = `\`
	}
	path := strings.TrimRight(folder, `/\`) + sep + filepath.Base(name)
	err = c.request(ctx, "SaveSourceScreenshot", map[string]any{"sourceName": source, "imageFormat": "png", "imageFilePath": path}, nil)
	return path, err
}

// sceneControlFor returns who set the current scene, for snapshots.
func (s *Service) control() (scene, setBy string, manualAt time.Time, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctl.scene, s.ctl.setBy, s.ctl.manualAt, s.ctl.known && s.cli != nil
}
