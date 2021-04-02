// Copyright (C) 2020  Raziman

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	spin "github.com/tj/go-spin"
	"github.com/tramhao/id3v2"
	"github.com/ztrue/tracerr"

	"github.com/issadarkthing/gomu/lyric"
	"github.com/issadarkthing/gomu/player"
)

var _ player.Audio = (*AudioFile)(nil)

// AudioFile represents directories and mp3 files
// isAudioFile equals to false if it is a directory
type AudioFile struct {
	name        string
	path        string
	isAudioFile bool
	length      time.Duration
	node        *tview.TreeNode
	parent      *tview.TreeNode
}

// Name return the name of AudioFile
func (a *AudioFile) Name() string {
	return a.name
}

// Path return the path of AudioFile
func (a *AudioFile) Path() string {
	return a.path
}

// IsAudioFile check if the file is song or directory
func (a *AudioFile) IsAudioFile() bool {
	return a.isAudioFile
}

// Len return the length of AudioFile
func (a *AudioFile) Len() time.Duration {
	return a.length
}

// GetParent return the parent directory of AudioFile
func (a *AudioFile) GetParent() *AudioFile {
	if a.parent == nil {
		return nil
	}
	return a.parent.GetReference().(*AudioFile)
}

func (a *AudioFile) String() string {
	if a == nil {
		return "nil"
	}
	return fmt.Sprintf("%#v", a)
}

// Playlist struct represents playlist panel
// that shows the tree of the music directory
type Playlist struct {
	*tview.TreeView
	prevNode     *tview.TreeNode
	defaultTitle string
	// number of downloads
	download int
	done     chan struct{}
	yankFile *AudioFile
}

func (p *Playlist) help() []string {

	return []string{
		"j      down",
		"k      up",
		"h      close node",
		"a      create a playlist",
		"l      add song to queue",
		"L      add playlist to queue",
		"d      delete file from filesystem",
		"D      delete playlist from filesystem",
		"Y      download audio from url",
		"r      refresh",
		"R      rename",
		"y/p    yank/paste file",
		"/      find in playlist",
		"s      search audio from youtube",
		"t      edit mp3 tags",
		"1/2    find lyric if available",
	}

}

// newPlaylist returns new instance of playlist and runs populate function
// on root music directory.
func newPlaylist(args Args) *Playlist {

	anko := gomu.anko

	m := anko.GetString("General.music_dir")
	rootDir, err := filepath.Abs(expandTilde(m))
	if err != nil {
		err = tracerr.Errorf("unable to find music directory: %e", err)
		die(err)
	}

	// if not default value was given
	if *args.music != "~/music" {
		rootDir = expandFilePath(*args.music)
	}

	var rootTextView string

	useEmoji := anko.GetBool("General.use_emoji")

	if useEmoji {

		emojiPlaylist := anko.GetString("Emoji.playlist")
		rootTextView = fmt.Sprintf("%s %s", emojiPlaylist, path.Base(rootDir))

	} else {
		rootTextView = path.Base(rootDir)
	}

	root := tview.NewTreeNode(rootTextView).
		SetColor(gomu.colors.playlistDir)

	tree := tview.NewTreeView().SetRoot(root)
	tree.SetBackgroundColor(gomu.colors.background)

	playlist := &Playlist{
		TreeView:     tree,
		defaultTitle: "─ Playlist ──┤ 0 downloads ├",
		done:         make(chan struct{}),
	}

	rootAudioFile := &AudioFile{
		name: path.Base(rootDir),
		node: root,
		path: rootDir,
	}

	root.SetReference(rootAudioFile)
	root.SetColor(gomu.colors.playlistDir)

	playlist.
		SetTitle(playlist.defaultTitle).
		SetBorder(true).
		SetTitleAlign(tview.AlignLeft).
		SetBorderPadding(0, 0, 1, 1)

	populate(root, rootDir, gomu.anko.GetBool("General.sort_by_mtime"))

	var firstChild *tview.TreeNode

	if len(root.GetChildren()) == 0 {
		firstChild = root
	} else {
		firstChild = root.GetChildren()[0]
	}

	playlist.setHighlight(firstChild)

	playlist.SetChangedFunc(func(node *tview.TreeNode) {
		playlist.setHighlight(node)
	})

	playlist.SetSelectedFunc(func(node *tview.TreeNode) {
		node.SetExpanded(!node.IsExpanded())
	})

	cmds := map[rune]string{
		'a': "create_playlist",
		'D': "delete_playlist",
		'd': "delete_file",
		'Y': "download_audio",
		's': "youtube_search",
		'l': "add_queue",
		'L': "bulk_add",
		'h': "close_node",
		'r': "refresh",
		'R': "rename",
		'y': "yank",
		'p': "paste",
		'/': "playlist_search",
		't': "edit_tags",
		'1': "fetch_lyric",
		'2': "fetch_lyric_cn2",
	}

	for key, cmdName := range cmds {
		src := fmt.Sprintf(`Keybinds.def_p("%c", %s)`, key, cmdName)
		anko.Execute(src)
	}

	playlist.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {

		if gomu.anko.KeybindExists("playlist", e) {

			err := gomu.anko.ExecKeybind("playlist", e)
			if err != nil {
				errorPopup(err)
			}

			return nil
		}

		// disable default key handler for space
		if e.Rune() == ' ' {
			return nil
		}

		return e
	})

	return playlist
}

// Returns the current file highlighted in the playlist
func (p Playlist) getCurrentFile() *AudioFile {
	node := p.GetCurrentNode()
	if node == nil {
		return nil
	}
	return node.GetReference().(*AudioFile)
}

// Deletes song from filesystem
func (p *Playlist) deleteSong(audioFile *AudioFile) (err error) {

	confirmationPopup(
		"Are you sure to delete this audio file?", func(_ int, buttonName string) {

			if buttonName == "no" || buttonName == "" {
				return
			}

			audioName := getName(audioFile.path)
			err := os.Remove(audioFile.path)

			if err != nil {

				defaultTimedPopup(" Error ", "Unable to delete "+audioFile.name)

				err = tracerr.Wrap(err)

			} else {

				defaultTimedPopup(" Success ",
					audioFile.name+"\nhas been deleted successfully")
				p.refresh()

				// Here we remove the song from queue
				songPaths := gomu.queue.getItems()
				if audioName == getName(gomu.player.GetCurrentSong().Name()) {
					gomu.player.Skip()
				}
				for i, songPath := range songPaths {
					if strings.Contains(songPath, audioName) {
						gomu.queue.deleteItem(i)
					}
				}
			}

		})

	return nil
}

// Deletes playlist/dir from filesystem
func (p *Playlist) deletePlaylist(audioFile *AudioFile) (err error) {

	err = os.RemoveAll(audioFile.path)
	if err != nil {
		err = tracerr.Wrap(err)
	} else {
		defaultTimedPopup(
			" Success ",
			audioFile.name+"\nhas been deleted successfully")
		p.refresh()
	}

	return err
}

// Bulk add a playlist to queue
func (p *Playlist) addAllToQueue(root *tview.TreeNode) {

	var childrens []*tview.TreeNode
	childrens = root.GetChildren()

	// gets the parent if the highlighted item is a file
	if root.GetReference().(*AudioFile).isAudioFile {
		childrens = root.GetReference().(*AudioFile).parent.GetChildren()
	}

	for _, v := range childrens {
		currNode := v.GetReference().(*AudioFile)
		if currNode.isAudioFile {

			currSong := gomu.player.GetCurrentSong()
			if currSong == nil || (currNode.Name() != currSong.Name()) {
				gomu.queue.enqueue(currNode)
			}

		}
	}

}

// Refreshes the playlist and read the whole root music dir
func (p *Playlist) refresh() {

	root := gomu.playlist.GetRoot()
	prevNode := gomu.playlist.GetCurrentNode()
	prevFilepath := prevNode.GetReference().(*AudioFile).Path()

	root.ClearChildren()
	node := root.GetReference().(*AudioFile)

	populate(root, node.path, gomu.anko.GetBool("General.sort_by_mtime"))

	root.Walk(func(node, _ *tview.TreeNode) bool {

		// to preserve previously highlighted node
		if node.GetReference().(*AudioFile).Path() == prevFilepath {
			p.setHighlight(node)
			return false
		}

		return true
	})

}

// Adds child while setting reference to audio file
func (p *Playlist) addSongToPlaylist(
	audioPath string, selPlaylist *tview.TreeNode,
) error {

	f, err := os.Open(audioPath)
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer f.Close()

	songName := getName(audioPath)
	node := tview.NewTreeNode(songName)

	// populateAudioLength(selPlaylist)
	audioLength, err := getTagLength(audioPath)
	if err != nil {
		return tracerr.Wrap(err)
	}

	audioFile := &AudioFile{
		name:        songName,
		path:        audioPath,
		isAudioFile: true,
		length:      audioLength,
		node:        node,
		parent:      selPlaylist,
	}

	displayText := setDisplayText(audioFile)

	node.SetReference(audioFile)
	node.SetText(displayText)
	selPlaylist.AddChild(node)

	return nil
}

// Gets all audio files walks from music root directory
func (p *Playlist) getAudioFiles() []*AudioFile {

	root := p.GetRoot()

	audioFiles := []*AudioFile{}

	root.Walk(func(node, _ *tview.TreeNode) bool {

		audioFile := node.GetReference().(*AudioFile)
		audioFiles = append(audioFiles, audioFile)

		return true
	})

	return audioFiles
}

// Creates a directory under selected node, returns error if playlist exists
func (p *Playlist) createPlaylist(name string) error {

	selectedNode := p.GetCurrentNode()

	parentNode := selectedNode.GetReference().(*AudioFile).parent

	// if the current node is the root
	// sets the parent to itself
	if parentNode == nil {
		parentNode = selectedNode
	}

	audioFile := parentNode.GetReference().(*AudioFile)

	err := os.Mkdir(path.Join(audioFile.path, name), 0744)

	if err != nil {
		return tracerr.Wrap(err)
	}

	p.refresh()

	return nil

}

// This is used to replace default behaviour of SetCurrentNode which
// adds color highlight attributes
func (p *Playlist) setHighlight(currNode *tview.TreeNode) {

	if p.prevNode != nil {
		if p.prevNode.GetReference().(*AudioFile).isAudioFile {
			p.prevNode.SetColor(gomu.colors.foreground)
		} else {
			p.prevNode.SetColor(gomu.colors.playlistDir)
		}
	}

	currNode.SetColor(gomu.colors.playlistHi)
	p.SetCurrentNode(currNode)

	p.prevNode = currNode
}

// Traverses the playlist and finds the AudioFile struct
// audioName must be hashed with sha1 first
func (p *Playlist) findAudioFile(audioName string) (*AudioFile, error) {

	root := p.GetRoot()

	var selNode *AudioFile

	root.Walk(func(node, _ *tview.TreeNode) bool {

		audioFile := node.GetReference().(*AudioFile)

		hashed := sha1Hex(getName(audioFile.name))

		if hashed == audioName {
			selNode = audioFile
			return false
		}

		return true
	})

	if selNode == nil {
		return nil, tracerr.New("no matching audio name")
	}

	return selNode, nil
}

func (p *Playlist) rename(newName string) error {

	currentNode := p.GetCurrentNode()
	audio := currentNode.GetReference().(*AudioFile)

	pathToFile, _ := filepath.Split(audio.path)
	var newPath string
	if audio.isAudioFile {
		newPath = pathToFile + newName + ".mp3"
	} else {
		newPath = pathToFile + newName
	}
	err := os.Rename(audio.path, newPath)
	if err != nil {
		return tracerr.Wrap(err)
	}

	return nil
}

// updateTitle creates a spinning motion on the title
// of the playlist panel when downloading.
func (p *Playlist) updateTitle() {

	if p.download == 0 {
		p.SetTitle(p.defaultTitle)
		return
	}

	// only one call can be made in one time
	if p.download > 1 {
		return
	}

	s := spin.New()

Download:
	for {

		select {
		case <-p.done:
			p.download--
			if p.download == 0 {
				p.SetTitle(p.defaultTitle)
				break Download
			}
		case <-time.After(time.Millisecond * 100):

			r, g, b := gomu.colors.accent.RGB()
			hexColor := padHex(r, g, b)

			title := fmt.Sprintf("─ Playlist ──┤ %d downloads [green]%s[#%s] ├",
				p.download, s.Next(), hexColor)
			p.SetTitle(title)
			gomu.app.Draw()
		}
	}

}

// Download audio from youtube audio and adds the song to the selected playlist
func ytdl(url string, selPlaylist *tview.TreeNode) error {

	// lookup if youtube-dl exists
	_, err := exec.LookPath("youtube-dl")

	if err != nil {
		defaultTimedPopup(" Error ", "youtube-dl is not in your $PATH")

		return tracerr.Wrap(err)
	}

	selAudioFile := selPlaylist.GetReference().(*AudioFile)
	dir := selAudioFile.path

	defaultTimedPopup(" Ytdl ", "Downloading")

	// specify the output path for ytdl
	outputDir := fmt.Sprintf(
		"%s/%%(title)s.%%(ext)s",
		dir)

	metaData := fmt.Sprintf("%%(artist)s - %%(title)s")

	args := []string{
		"--extract-audio",
		"--audio-format",
		"mp3",
		"--output",
		outputDir,
		"--add-metadata",
		"--embed-thumbnail",
		"--metadata-from-title",
		metaData,
		"--write-sub",
		"--all-subs",
		"--convert-subs",
		"lrc",
		// "--cookies",
		// "~/Downloads/youtube.com_cookies.txt",
		url,
	}

	cmd := exec.Command("youtube-dl", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	gomu.playlist.download++
	go gomu.playlist.updateTitle()

	// blocking
	err = cmd.Run()

	gomu.playlist.done <- struct{}{}

	if err != nil {
		return tracerr.Wrap(err)
	}

	playlistPath := dir
	audioPath := extractFilePath(stdout.Bytes(), playlistPath)

	historyPath := gomu.anko.GetString("General.history_path")

	err = appendFile(expandTilde(historyPath), url+"\n")
	if err != nil {
		return tracerr.Wrap(err)
	}

	err = gomu.playlist.addSongToPlaylist(audioPath, selPlaylist)
	if err != nil {
		return tracerr.Wrap(err)
	}

	// Embed lyric to mp3 as uslt
	var tag *id3v2.Tag
	tag, err = id3v2.Open(audioPath, id3v2.Options{Parse: true})
	if err != nil {
		return tracerr.Wrap(err)
	}
	defer tag.Close()

	pathToFile, _ := filepath.Split(audioPath)
	files, err := ioutil.ReadDir(pathToFile)
	if err != nil {
		logError(err)
	}
	var lyricWritten int = 0
	for _, file := range files {
		fileName := file.Name()
		fileExt := filepath.Ext(fileName)
		lyricFileName := filepath.Join(pathToFile, fileName)
		if fileExt == ".lrc" {
			// Embed all lyrics and use langExt as content descriptor of uslt
			fileNameWithoutExt := strings.TrimSuffix(fileName, fileExt)
			langExt := strings.TrimPrefix(filepath.Ext(fileNameWithoutExt), ".")

			// Read entire file content, giving us little control but
			// making it very simple. No need to close the file.
			byteContent, err := ioutil.ReadFile(lyricFileName)
			if err != nil {
				return tracerr.Wrap(err)
			}
			lyricContent := string(byteContent)

			var lyric lyric.Lyric
			err = lyric.NewFromLRC(lyricContent)
			if err != nil {
				return tracerr.Wrap(err)
			}
			lyric.LangExt = langExt
			err = embedLyric(audioPath, &lyric, false)
			if err != nil {
				return tracerr.Wrap(err)
			}
			err = os.Remove(lyricFileName)
			if err != nil {
				return tracerr.Wrap(err)
			}
			lyricWritten++
		}
	}

	downloadFinishedMessage := fmt.Sprintf("Finished downloading\n%s\n%v lyrics embeded", getName(audioPath), lyricWritten)
	defaultTimedPopup(" Ytdl ", downloadFinishedMessage)
	gomu.app.Draw()

	return nil
}

// Add songs and their directories in Playlist panel.
func populate(root *tview.TreeNode, rootPath string, sortMtime bool) error {

	files, err := ioutil.ReadDir(rootPath)

	if err != nil {
		return tracerr.Wrap(err)
	}

	if sortMtime {
		sort.Slice(files, func(i, j int) bool {
			stat1 := files[i].Sys().(*syscall.Stat_t)
			stat2 := files[j].Sys().(*syscall.Stat_t)

			time1 := time.Unix(stat1.Mtim.Unix())
			time2 := time.Unix(stat2.Mtim.Unix())

			return time1.After(time2)
		})
	}

	for _, file := range files {

		path, err := filepath.EvalSymlinks(filepath.Join(rootPath, file.Name()))
		if err != nil {
			continue
		}

		songName := getName(file.Name())
		child := tview.NewTreeNode(songName)

		if file.Mode().IsRegular() {

			f, err := os.Open(path)
			if err != nil {
				continue
			}
			defer f.Close()

			filetype, err := getFileContentType(f)

			if err != nil {
				continue
			}

			// skip if not mp3 file
			if filetype != "mpeg" {
				continue
			}

			audioFile := &AudioFile{
				name:        songName,
				path:        path,
				isAudioFile: true,
				node:        child,
				parent:      root,
			}

			audioLength, err := getTagLength(audioFile.path)
			if err != nil {
				logError(err)
			}

			audioFile.length = audioLength

			displayText := setDisplayText(audioFile)

			child.SetReference(audioFile)
			child.SetText(displayText)
			root.AddChild(child)

		}

		if file.IsDir() || file.Mode()&os.ModeSymlink != 0 {

			audioFile := &AudioFile{
				name:        songName,
				path:        path,
				isAudioFile: false,
				node:        child,
				parent:      root,
			}

			displayText := setDisplayText(audioFile)

			child.SetReference(audioFile)
			child.SetColor(gomu.colors.playlistDir)
			child.SetText(displayText)
			root.AddChild(child)
			populate(child, path, sortMtime)

		}

	}

	return nil
}

func (p *Playlist) yank() error {
	p.yankFile = p.getCurrentFile()
	if p.yankFile == nil {
		return errors.New("no file has been yanked")
	}
	if p.yankFile.node == p.GetRoot() {
		return errors.New("please don't yank the root directory")
	}
	defaultTimedPopup(" Success ", p.yankFile.name+"\n has been yanked successfully.")

	return nil
}

func (p *Playlist) paste() error {
	if p.yankFile == nil {
		return errors.New("no file has been yanked")
	}

	oldPathDir, oldPathFileName := filepath.Split(p.yankFile.path)
	pasteFile := p.getCurrentFile()
	var newPathDir string
	if pasteFile.isAudioFile {
		newPathDir, _ = filepath.Split(pasteFile.path)
	} else {
		newPathDir = pasteFile.path
	}

	if oldPathDir == newPathDir {
		return nil
	}

	newPathFull := filepath.Join(newPathDir, oldPathFileName)
	err := os.Rename(p.yankFile.path, newPathFull)
	if err != nil {
		return tracerr.Wrap(err)
	}

	defaultTimedPopup(" Success ", p.yankFile.name+"\n has been pasted to\n"+newPathDir)

	// keep queue references updated
	p.refresh()
	gomu.queue.saveQueue(false)
	gomu.queue.clearQueue()
	gomu.queue.loadQueue()
	gomu.player.Skip()
	// if gomu.queue.isLoop {
	// 	_, err = gomu.queue.deleteItem(len(gomu.queue.items) - 1)
	// 	if err != nil {
	// 		return tracerr.Wrap(err)
	// 	}
	// }

	// currentSong := gomu.player.GetCurrentSong()
	// if p.yankFile.name == currentSong.Name() {
	// 	newNode := p.yankFile
	// 	newNode.path = newPathFull
	// 	gomu.queue.pushFront(newNode)
	// 	gomu.player.Skip()
	// 	if gomu.queue.isLoop {
	// 		_, err = gomu.queue.deleteItem(len(gomu.queue.items) - 1)
	// 		if err != nil {
	// 			return tracerr.Wrap(err)
	// 		}
	// 	}
	// }

	p.yankFile = nil

	return nil
}

func setDisplayText(audioFile *AudioFile) string {
	useEmoji := gomu.anko.GetBool("General.use_emoji")
	if !useEmoji {
		return audioFile.name
	}

	if audioFile.isAudioFile {
		emojiFile := gomu.anko.GetString("Emoji.file")
		return fmt.Sprintf(" %s %s", emojiFile, audioFile.name)
	}

	emojiDir := gomu.anko.GetString("Emoji.playlist")
	return fmt.Sprintf(" %s %s", emojiDir, audioFile.name)
}

func (p *Playlist) refreshByNode(node *AudioFile, newName string) error {

	root := p.GetRoot()
	root.Walk(func(node, _ *tview.TreeNode) bool {
		if strings.Contains(node.GetText(), newName) {
			p.setHighlight(node)
		}
		return true
	})
	// update queue
	if node.isAudioFile {
		newNode := p.getCurrentFile()
		err := gomu.queue.rename(node, newNode)
		if err != nil {
			return tracerr.Wrap(err)
		}

		if !gomu.player.IsRunning() {
			return nil
		}

		currentSong := gomu.player.GetCurrentSong()
		if node.name == currentSong.Name() {
			gomu.queue.enqueue(newNode)
			gomu.queue.pushFront(newNode)
			gomu.player.Skip()
			if gomu.queue.isLoop {
				_, err = gomu.queue.deleteItem(len(gomu.queue.items) - 1)
				if err != nil {
					return tracerr.Wrap(err)
				}
			}
		}

	} else {
		gomu.queue.saveQueue(false)
		gomu.queue.clearQueue()
		gomu.queue.loadQueue()
	}
	return nil
}
