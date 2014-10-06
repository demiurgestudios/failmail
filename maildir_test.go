package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"testing"
	"time"
)

func TestCreate(t *testing.T) {
	m, cleanup := makeTestMaildir(t)
	defer cleanup()

	if stat, err := os.Stat(path.Join(m.Path, "cur")); err != nil {
		t.Errorf("$maildir/cur not found: %s", err)
	} else if !stat.IsDir() {
		t.Errorf("$maildir/cur not a dir")
	} else if stat.Mode().Perm() != 0755 {
		t.Errorf("$maildir/cur bad mode: %s", stat.Mode())
	}
}

func TestCreateInvalidDir(t *testing.T) {
	m := &Maildir{Path: "/does-not-exist"}

	if err := m.Create(); err == nil {
		t.Errorf("expected an error from Create")
	}
}

func TestNextUniqueName(t *testing.T) {
	m, cleanup := makeTestMaildir(t)
	defer cleanup()

	defer patchHost("test", nil)()
	defer patchTime(time.Unix(1393650000, 0))()
	defer patchPid(1000)()

	if name, err := m.NextUniqueName(); err != nil {
		t.Errorf("unexpected error for NextUniqueName(): %s", err)
	} else if name != "1393650000.1000_1.test" {
		t.Errorf("unexpected name for NextUniqueName(): %s", name)
	}

	if name, err := m.NextUniqueName(); err != nil {
		t.Errorf("unexpected error for NextUniqueName(): %s", err)
	} else if name != "1393650000.1000_2.test" {
		t.Errorf("unexpected name for NextUniqueName(): %s", name)
	}
}

func TestWrite(t *testing.T) {
	m, cleanup := makeTestMaildir(t)
	defer cleanup()

	defer patchHost("test", nil)()
	defer patchTime(time.Unix(1393650000, 0))()
	defer patchPid(1000)()

	if name, err := m.Write([]byte("test mail")); err != nil {
		t.Errorf("couldn't write to maildir: %s", err)
	} else if name != "1393650000.1000_1.test:2,S" {
		t.Errorf("unexpected returned name: %s", name)
	} else if entries, err := ioutil.ReadDir(path.Join(m.Path, "cur")); err != nil {
		t.Fatalf("couldn't get entries for maildir: %s", err)
	} else if len(entries) != 1 {
		t.Errorf("expected %d entries, found %d", 1, len(entries))
	} else if entries[0].Name() != "1393650000.1000_1.test:2,S" {
		t.Errorf("unexpected name: %s", entries[0].Name())
	} else if entries[0].Size() != int64(len("test mail")) {
		t.Errorf("unexpected size: %d", entries[0].Size())
	}
}

func TestHostnameError(t *testing.T) {
	m, cleanup := makeTestMaildir(t)
	defer cleanup()

	defer patchHost("", fmt.Errorf("couldn't get hostname"))()

	if _, err := m.Write([]byte("test mail")); err == nil {
		t.Errorf("expected an error writing to maildir")
	} else if err.Error() != "couldn't get hostname" {
		t.Errorf("expected a different error writing to maildir")
	}
}

func TestList(t *testing.T) {
	m, cleanup := makeTestMaildir(t)
	defer cleanup()

	defer patchHost("test", nil)()
	defer patchTime(time.Unix(1393650000, 0))()
	defer patchPid(1000)()

	if _, err := m.Write([]byte("From: test@example.com\r\nSubject: test\r\n\r\ntest body")); err != nil {
		t.Errorf("couldn't write to maildir: %s", err)
	}

	items, err := m.List()
	if err != nil {
		t.Errorf("unexpected error listing messages: %s", err)
	} else if !reflect.DeepEqual(items, []string{"1393650000.1000_1.test:2,S"}) {
		t.Errorf("unexpected messages in list: %v", items)
	}
}

func TestListInvalidDir(t *testing.T) {
	m := &Maildir{Path: "/does-not-exist"}
	files, err := m.List()

	if err == nil {
		t.Errorf("expected an error from List")
	}

	if len(files) != 0 {
		t.Errorf("expected an empty slice from List")
	}
}

func TestRead(t *testing.T) {
	m, cleanup := makeTestMaildir(t)
	defer cleanup()

	defer patchHost("test", nil)()
	defer patchTime(time.Unix(1393650000, 0))()
	defer patchPid(1000)()

	if _, err := m.Write([]byte("From: test@example.com\r\nSubject: test\r\n\r\ntest body")); err != nil {
		t.Errorf("couldn't write to maildir: %s", err)
	}

	msg, err := m.Read("1393650000.1000_1.test:2,S")
	if err != nil {
		t.Errorf("unexpected error reading message: %s", err)
	} else if subj := msg.Header.Get("Subject"); subj != "test" {
		t.Errorf("unexpected subject for message: %s", subj)
	}
}

func makeTestMaildir(t *testing.T) (*Maildir, func()) {
	tmp, err := ioutil.TempDir("", "maildir")
	if err != nil {
		t.Fatalf("couldn't create temp dir: %s", err)
	}

	m := &Maildir{Path: path.Join(tmp, "test")}
	if err := m.Create(); err != nil {
		t.Fatalf("error creating maildir %v: %s", m, err)
	}

	return m, func() { os.RemoveAll(tmp) }
}
