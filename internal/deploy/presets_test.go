package deploy

import "testing"

func TestDetectPreset(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"next", []string{"package.json", "next.config.js", "tsconfig.json"}, PresetNext},
		{"next mjs", []string{"package.json", "next.config.mjs"}, PresetNext},
		{"laravel", []string{"composer.json", "artisan", "package.json"}, PresetLaravel},
		{"php", []string{"composer.json", "index.php"}, PresetPHP},
		{"node", []string{"package.json", "index.js"}, PresetNode},
		{"go", []string{"go.mod", "main.go"}, PresetGo},
		{"python requirements", []string{"requirements.txt", "app.py"}, PresetPython},
		{"python pyproject", []string{"pyproject.toml"}, PresetPython},
		{"static", []string{"index.html", "style.css"}, PresetStatic},
		{"unknown", []string{"README.md"}, PresetStatic},
		{"empty", nil, PresetStatic},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DetectPreset(c.files); got != c.want {
				t.Errorf("DetectPreset(%v) = %q, want %q", c.files, got, c.want)
			}
		})
	}
}

func TestPresetStepsCoverAllKeys(t *testing.T) {
	for _, preset := range append(Presets, "unknown-preset") {
		steps := PresetSteps(preset)
		if len(steps) != len(StepKeys) {
			t.Fatalf("preset %q: %d steps, want %d", preset, len(steps), len(StepKeys))
		}
		for i, k := range StepKeys {
			if steps[i].Key != k {
				t.Errorf("preset %q: step %d key = %q, want %q", preset, i, steps[i].Key, k)
			}
			if !ValidStepKey(steps[i].Key) {
				t.Errorf("preset %q: step key %q is not valid", preset, steps[i].Key)
			}
		}
	}
}
