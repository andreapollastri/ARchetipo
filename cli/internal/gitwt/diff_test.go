package gitwt

import "testing"

func TestParseUnifiedDiff_Modified(t *testing.T) {
	in := `diff --git a/foo.go b/foo.go
index 1234567..89abcde 100644
--- a/foo.go
+++ b/foo.go
@@ -1,4 +1,4 @@
 package main
-var x = 1
+var x = 2
 // tail
`
	files := parseUnifiedDiff(in)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != "modified" || f.NewPath != "foo.go" || f.OldPath != "foo.go" {
		t.Fatalf("unexpected file meta: %+v", f)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(f.Hunks))
	}
	var del, add *Line
	for i := range f.Hunks[0].Lines {
		l := &f.Hunks[0].Lines[i]
		if l.Kind == "del" {
			del = l
		}
		if l.Kind == "add" {
			add = l
		}
	}
	if del == nil || del.OldLine != 2 || del.Text != "var x = 1" {
		t.Fatalf("unexpected del line: %+v", del)
	}
	if add == nil || add.NewLine != 2 || add.Text != "var x = 2" {
		t.Fatalf("unexpected add line: %+v", add)
	}
}

func TestParseUnifiedDiff_AddedAndDeleted(t *testing.T) {
	in := `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..e69de29
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+hello
+world
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index e69de29..0000000
--- a/gone.txt
+++ /dev/null
@@ -1,1 +0,0 @@
-bye
`
	files := parseUnifiedDiff(in)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Status != "added" || files[0].NewPath != "new.txt" {
		t.Fatalf("unexpected added file: %+v", files[0])
	}
	if files[0].Hunks[0].Lines[1].NewLine != 2 {
		t.Fatalf("want second added line NewLine=2, got %d", files[0].Hunks[0].Lines[1].NewLine)
	}
	if files[1].Status != "deleted" || files[1].OldPath != "gone.txt" {
		t.Fatalf("unexpected deleted file: %+v", files[1])
	}
}

func TestParseUnifiedDiff_Rename(t *testing.T) {
	in := `diff --git a/old.go b/new.go
similarity index 90%
rename from old.go
rename to new.go
index 1234567..89abcde 100644
--- a/old.go
+++ b/new.go
@@ -3,3 +3,3 @@ func f() {
 a
-b
+c
 d
`
	files := parseUnifiedDiff(in)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Status != "renamed" || files[0].OldPath != "old.go" || files[0].NewPath != "new.go" {
		t.Fatalf("unexpected rename meta: %+v", files[0])
	}
	// hunk starts at old/new line 3
	if files[0].Hunks[0].Lines[0].OldLine != 3 || files[0].Hunks[0].Lines[0].NewLine != 3 {
		t.Fatalf("unexpected first line numbers: %+v", files[0].Hunks[0].Lines[0])
	}
}

func TestParseUnifiedDiff_MultiHunk(t *testing.T) {
	in := `diff --git a/m.go b/m.go
--- a/m.go
+++ b/m.go
@@ -1,2 +1,2 @@
-a
+A
 b
@@ -10,2 +10,3 @@
 j
+k
 l
`
	files := parseUnifiedDiff(in)
	if len(files[0].Hunks) != 2 {
		t.Fatalf("want 2 hunks, got %d", len(files[0].Hunks))
	}
	// second hunk's added line should be at new line 11 (start 10: j=10, k=11)
	var k *Line
	for i := range files[0].Hunks[1].Lines {
		if files[0].Hunks[1].Lines[i].Kind == "add" {
			k = &files[0].Hunks[1].Lines[i]
		}
	}
	if k == nil || k.NewLine != 11 {
		t.Fatalf("unexpected added line in second hunk: %+v", k)
	}
}
