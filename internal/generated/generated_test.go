package generated

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		path, category, rule string
		confidence           Confidence
	}{
		{"node_modules/pkg/index.js", "dependencies", "node_modules", Strong},
		{"src/__pycache__/module", "cache", "__pycache__", Strong},
		{".next/server/page", "build-output", ".next", Strong},
		{".nuxt/app", "build-output", ".nuxt", Strong},
		{".gradle/state", "cache", ".gradle", Strong},
		{".pytest_cache/state", "cache", ".pytest_cache", Strong},
		{".mypy_cache/state", "cache", ".mypy_cache", Strong},
		{"coverage/report", "coverage", "coverage", Weak},
		{".nyc_output/result", "coverage", ".nyc_output", Strong},
		{"htmlcov/index.html", "coverage", "htmlcov", Strong},
		{".terraform/provider", "dependencies", ".terraform", Strong},
		{"src/a.pyc", "cache", "*.pyc", Strong},
		{"src/a.pyo", "cache", "*.pyo", Strong},
		{"a.class", "build-output", "*.class", Strong},
		{"a.o", "build-output", "*.o", Weak},
		{"a.obj", "build-output", "*.obj", Weak},
		{"a.exe", "build-output", "*.exe", Weak},
		{"a.dll", "build-output", "*.dll", Weak},
		{"a.so", "build-output", "*.so", Weak},
		{"a.dylib", "build-output", "*.dylib", Weak},
		{"app.min.js", "build-output", "*.min.js", Weak},
		{"app.min.css", "build-output", "*.min.css", Weak},
		{"app.js.map", "build-output", "*.map", Weak},
		{"server.log", "logs", "*.log", Weak},
		{".DS_Store", "metadata", ".DS_Store", Strong},
		{"src/.DS_STORE", "metadata", ".DS_Store", Strong},
		{"src/.ds_store", "metadata", ".DS_Store", Strong},
		{"Thumbs.db", "metadata", "Thumbs.db", Strong},
		{"src/THUMBS.DB", "metadata", "Thumbs.db", Strong},
		{"src/thumbs.db", "metadata", "Thumbs.db", Strong},
		{"dist/app.js", "build-output", "dist", Weak},
		{"build/x.js", "build-output", "build", Weak},
		{"src/build/x.js", "build-output", "build", Weak},
		{"out/file", "build-output", "out", Weak},
		{"target/file", "build-output", "target", Weak},
		{"vendor/pkg/file", "dependencies", "vendor", Weak},
		{"bin/tool", "build-output", "bin", Weak},
		{"obj/file", "build-output", "obj", Weak},
		{".cache/file", "cache", ".cache", Weak},
		{"build/", "build-output", "build", Weak},
		{`src\build\x.js`, "build-output", "build", Weak},
		{`src\node_modules\pkg\a.js`, "dependencies", "node_modules", Strong},
		{`src\THUMBS.DB`, "metadata", "Thumbs.db", Strong},
		{"./src/build/x.js", "build-output", "build", Weak},
		{`.\src\__pycache__\module`, "cache", "__pycache__", Strong},
		{"./app.min.js", "build-output", "*.min.js", Weak},
		{"build/src/a.log", "logs", "*.log", Weak},
		{"dist/a.pyc", "cache", "*.pyc", Strong},
		{"node_modules/a.log", "dependencies", "node_modules", Strong},
		{"dist/node_modules/pkg/index.js", "dependencies", "node_modules", Strong},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := Classify(tt.path)
			want := Match{tt.category, tt.confidence, tt.rule}
			if !ok || got != want {
				t.Fatalf("Classify(%q) = (%+v, %t), want (%+v, true)", tt.path, got, ok, want)
			}
		})
	}
}

func TestClassifyNegatives(t *testing.T) {
	for _, path := range []string{
		"", ".", "./", "rebuild/x.js", "builder.go", "distribution/x.js",
		"src/target.go", "src/build.go", "src/vendor.json", "src/a.bin",
		"build", "src/build", "dist", "vendor", ".cache",
		"NODE_MODULES/pkg/index.js", "Build/x.js", "Coverage/report",
		"node_modules_extra/a.js", "src/prefixnode_modules/a.js",
		"__pycache__extra/a", ".nextjs/app", "src/a.PYC", "src/a.EXE",
		"app.MIN.js", "app.min.JS", "server.LOG", "a.pyc.txt",
		"DS_STORE", ".DS_Store.backup", "Thumbs.db.backup",
		".DS_Store/readme.txt", "Thumbs.db/readme.txt", "src.pyc/readme.txt",
		`src\rebuild\x.js`, "src/cache/x", "src/obj.go", "src/out.txt",
	} {
		t.Run(path, func(t *testing.T) {
			if got, ok := Classify(path); ok || got != (Match{}) {
				t.Fatalf("Classify(%q) = (%+v, %t), want zero match, false", path, got, ok)
			}
		})
	}
}
