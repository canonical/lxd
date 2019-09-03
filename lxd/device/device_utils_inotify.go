package device

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/lxc/lxd/lxd/state"
	"github.com/lxc/lxd/lxd/sys"
	"github.com/lxc/lxd/shared"
	log "github.com/lxc/lxd/shared/log15"
	"github.com/lxc/lxd/shared/logger"
)

const inotifyBatchInEvents uint = 100
const inotifySingleInEventSize uint = (unix.SizeofInotifyEvent + unix.PathMax)
const inotifyBatchInBufSize uint = inotifyBatchInEvents * inotifySingleInEventSize

// InotifyInit initialises the inotify internal structures.
func InotifyInit(s *state.State) (int, error) {
	s.OS.InotifyWatch.Lock()
	defer s.OS.InotifyWatch.Unlock()

	if s.OS.InotifyWatch.Fd >= 0 {
		return s.OS.InotifyWatch.Fd, nil
	}

	inFd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		logger.Errorf("Failed to initialize inotify")
		return -1, err
	}
	logger.Debugf("Initialized inotify with file descriptor %d", inFd)

	s.OS.InotifyWatch.Fd = inFd

	return inFd, nil
}

// InotifyHandler starts watching for inotify events.
func InotifyHandler(s *state.State) {
	watchChan, err := inotifyWatcher(s)
	if err != nil {
		return
	}

	for {
		select {
		case v := <-watchChan:
			inotifyEvent(s, &v)
		}
	}
}

// inotifyDirRescan tries to add inotify watchers for all subscribed paths. It generates pseudo
// events to any registered handler with either add or remove action based on whether or not the
// file exists.
func inotifyDirRescan(s *state.State) {
	// Because we don't know what sort of event actually occurred, lets generate a pseudo event
	// for each of the unique paths that devices for all instances on this host are subcribed
	// to. This will test for whether each file actually exists or not and allow the handler
	// functions to add/remove as necessary. It also allows inotify watchers to be setup for
	// all desired paths.
	subs := unixGetSubcribedPaths()
	for subPath := range subs {
		cleanDevPath := filepath.Clean(subPath)
		e := unixNewEvent("", subPath)
		unixRunHandlers(s, &e)

		// Add its nearest existing ancestor.
		err := inotifyAddClosestLivingAncestor(s, filepath.Dir(cleanDevPath))
		if err != nil {
			logger.Errorf("Failed to add \"%s\" to inotify targets: %s", filepath.Dir(cleanDevPath), err)
		} else {
			logger.Debugf("Added \"%s\" to inotify targets", filepath.Dir(cleanDevPath))
		}
	}
}

func inotifyFindClosestLivingAncestor(cleanPath string) (bool, string) {
	if shared.PathExists(cleanPath) {
		return true, cleanPath
	}

	s := cleanPath
	for {
		s = filepath.Dir(s)
		if s == cleanPath {
			return false, s
		}
		if shared.PathExists(s) {
			return true, s
		}
	}
}

func inotifyAddClosestLivingAncestor(s *state.State, path string) error {
	cleanPath := filepath.Clean(path)
	// Find first existing ancestor directory and add it to the target.
	exists, watchDir := inotifyFindClosestLivingAncestor(cleanPath)
	if !exists {
		return fmt.Errorf("No existing ancestor directory found for \"%s\"", path)
	}

	err := inotifyAddTarget(s, watchDir)
	if err != nil {
		return err
	}

	return nil
}

func inotifyAddTarget(s *state.State, path string) error {
	s.OS.InotifyWatch.Lock()
	defer s.OS.InotifyWatch.Unlock()

	inFd := s.OS.InotifyWatch.Fd
	if inFd < 0 {
		return fmt.Errorf("Inotify instance not intialized")
	}

	// Do not add the same target twice.
	_, ok := s.OS.InotifyWatch.Targets[path]
	if ok {
		logger.Debugf("Inotify is already watching \"%s\"", path)
		return nil
	}

	mask := uint32(0)
	mask |= unix.IN_ONLYDIR
	mask |= unix.IN_CREATE
	mask |= unix.IN_DELETE
	mask |= unix.IN_DELETE_SELF
	wd, err := unix.InotifyAddWatch(inFd, path, mask)
	if err != nil {
		return err
	}

	s.OS.InotifyWatch.Targets[path] = &sys.InotifyTargetInfo{
		Mask: mask,
		Path: path,
		Wd:   wd,
	}

	// Add a second key based on the watch file descriptor to the map that
	// points to the same allocated memory. This is used to reverse engineer
	// the absolute path when an event happens in the watched directory.
	// We prefix the key with a \0 character as this is disallowed in
	// directory and file names and thus guarantees uniqueness of the key.
	wdString := fmt.Sprintf("\000:%d", wd)
	s.OS.InotifyWatch.Targets[wdString] = s.OS.InotifyWatch.Targets[path]
	return nil
}

func inotifyDel(s *state.State) {
	s.OS.InotifyWatch.Lock()
	unix.Close(s.OS.InotifyWatch.Fd)
	s.OS.InotifyWatch.Fd = -1
	s.OS.InotifyWatch.Unlock()
}

func inotifyWatcher(s *state.State) (chan sys.InotifyTargetInfo, error) {
	targetChan := make(chan sys.InotifyTargetInfo)
	go func(target chan sys.InotifyTargetInfo) {
		for {
			buf := make([]byte, inotifyBatchInBufSize)
			n, errno := unix.Read(s.OS.InotifyWatch.Fd, buf)
			if errno != nil {
				if errno == unix.EINTR {
					continue
				}

				inotifyDel(s)
				return
			}

			if n < unix.SizeofInotifyEvent {
				continue
			}

			var offset uint32
			for offset <= uint32(n-unix.SizeofInotifyEvent) {
				name := ""
				event := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset]))

				nameLen := uint32(event.Len)
				if nameLen > 0 {
					bytes := (*[unix.PathMax]byte)(unsafe.Pointer(&buf[offset+unix.SizeofInotifyEvent]))
					name = strings.TrimRight(string(bytes[0:nameLen]), "\000")
				}

				target <- sys.InotifyTargetInfo{
					Mask: uint32(event.Mask),
					Path: name,
					Wd:   int(event.Wd),
				}

				offset += (unix.SizeofInotifyEvent + nameLen)
			}
		}
	}(targetChan)

	return targetChan, nil
}

func inotifyDelWatcher(s *state.State, path string) error {
	s.OS.InotifyWatch.Lock()
	defer s.OS.InotifyWatch.Unlock()

	if s.OS.InotifyWatch.Fd < 0 {
		return nil
	}

	target, ok := s.OS.InotifyWatch.Targets[path]
	if !ok {
		logger.Debugf("Inotify target \"%s\" not present", path)
		return nil
	}

	ret, err := unix.InotifyRmWatch(s.OS.InotifyWatch.Fd, uint32(target.Wd))
	if ret != 0 {
		// When a file gets deleted the wd for that file will
		// automatically be deleted from the inotify instance. So
		// ignore errors here.
		logger.Debugf("Inotify syscall returned %s for \"%s\"", err, path)
	}
	delete(s.OS.InotifyWatch.Targets, path)
	wdString := fmt.Sprintf("\000:%d", target.Wd)
	delete(s.OS.InotifyWatch.Targets, wdString)
	return nil
}

func inotifyCreateAncestorPaths(cleanPath string) []string {
	components := strings.Split(cleanPath, "/")
	ancestors := []string{}
	newPath := "/"
	ancestors = append(ancestors, newPath)
	for _, v := range components[1:] {
		newPath = filepath.Join(newPath, v)
		ancestors = append(ancestors, newPath)
	}

	return ancestors
}

func inotifyEvent(s *state.State, target *sys.InotifyTargetInfo) {
	if (target.Mask & unix.IN_ISDIR) > 0 {
		if (target.Mask & unix.IN_CREATE) > 0 {
			inotifyDirCreateEvent(s, target)
		} else if (target.Mask & unix.IN_DELETE) > 0 {
			inotifyDirDeleteEvent(s, target)
		}
		inotifyDirRescan(s)
	} else if (target.Mask & unix.IN_DELETE_SELF) > 0 {
		inotifyDirDeleteEvent(s, target)
		inotifyDirRescan(s)
	} else {
		inotifyFileEvent(s, target)
	}
}

func inotifyDirDeleteEvent(s *state.State, target *sys.InotifyTargetInfo) {
	parentKey := fmt.Sprintf("\000:%d", target.Wd)
	s.OS.InotifyWatch.RLock()
	parent, ok := s.OS.InotifyWatch.Targets[parentKey]
	s.OS.InotifyWatch.RUnlock()
	if !ok {
		return
	}

	// The absolute path of the file for which we received an event?
	targetName := filepath.Join(parent.Path, target.Path)
	targetName = filepath.Clean(targetName)
	err := inotifyDelWatcher(s, targetName)
	if err != nil {
		logger.Errorf("Failed to remove \"%s\" from inotify targets: %s", targetName, err)
	} else {
		logger.Debugf("Removed \"%s\" from inotify targets", targetName)
	}
}

func inotifyDirCreateEvent(s *state.State, target *sys.InotifyTargetInfo) {
	parentKey := fmt.Sprintf("\000:%d", target.Wd)
	s.OS.InotifyWatch.RLock()
	parent, ok := s.OS.InotifyWatch.Targets[parentKey]
	s.OS.InotifyWatch.RUnlock()
	if !ok {
		return
	}

	// The absolute path of the file for which we received an event?
	targetName := filepath.Join(parent.Path, target.Path)
	targetName = filepath.Clean(targetName)

	// ancestors
	del := inotifyCreateAncestorPaths(targetName)
	keep := []string{}

	subs := unixGetSubcribedPaths()
	for subPath := range subs {
		cleanDevPath := filepath.Clean(subPath)

		for i := len(del) - 1; i >= 0; i-- {
			// Only keep paths that can be deleted.
			if strings.HasPrefix(cleanDevPath, del[i]) {
				if shared.StringInSlice(del[i], keep) {
					break
				}

				keep = append(keep, del[i])
				break
			}
		}
	}

	for i, v := range del {
		if shared.StringInSlice(v, keep) {
			del[i] = ""
		}
	}

	for _, v := range del {
		if v == "" {
			continue
		}

		err := inotifyDelWatcher(s, v)
		if err != nil {
			logger.Errorf("Failed to remove \"%s\" from inotify targets: %s", v, err)
		} else {
			logger.Debugf("Removed \"%s\" from inotify targets", v)
		}
	}

	for _, v := range keep {
		if v == "" {
			continue
		}

		err := inotifyAddClosestLivingAncestor(s, v)
		if err != nil {
			logger.Errorf("Failed to add \"%s\" to inotify targets: %s", v, err)
		} else {
			logger.Debugf("Added \"%s\" to inotify targets", v)
		}
	}
}

func inotifyFileEvent(s *state.State, target *sys.InotifyTargetInfo) {
	parentKey := fmt.Sprintf("\000:%d", target.Wd)
	s.OS.InotifyWatch.RLock()
	parent, ok := s.OS.InotifyWatch.Targets[parentKey]
	s.OS.InotifyWatch.RUnlock()
	if !ok {
		return
	}

	// Does the current file have watchers?
	hasWatchers := false
	// The absolute path of the file for which we received an event?
	targetName := filepath.Join(parent.Path, target.Path)

	subs := unixGetSubcribedPaths()
	for subPath := range subs {
		cleanDevPath := filepath.Clean(subPath)
		cleanInotPath := filepath.Clean(targetName)
		if !hasWatchers && strings.HasPrefix(cleanDevPath, cleanInotPath) {
			hasWatchers = true
		}

		if cleanDevPath != cleanInotPath {
			continue
		}

		if (target.Mask & unix.IN_CREATE) > 0 {
			e := unixNewEvent("add", cleanInotPath)
			unixRunHandlers(s, &e)
		} else if (target.Mask & unix.IN_DELETE) > 0 {
			e := unixNewEvent("remove", cleanInotPath)
			unixRunHandlers(s, &e)
		} else {
			logger.Error("Uknown action for unix device", log.Ctx{"dev": subPath, "target": cleanInotPath})
		}
	}

	if !hasWatchers {
		err := inotifyDelWatcher(s, targetName)
		if err != nil {
			logger.Errorf("Failed to remove \"%s\" from inotify targets: %s", targetName, err)
		} else {
			logger.Debugf("Removed \"%s\" from inotify targets", targetName)
		}
	}
}
