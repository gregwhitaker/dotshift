class Dotshift < Formula
  desc "Local coding, model fallback, tests, and independent review"
  homepage "https://github.com/gregwhitaker/dotshift"
  license "Apache-2.0"
  head "https://github.com/gregwhitaker/dotshift.git", branch: "main"

  depends_on "go" => :build

  uses_from_macos "git"

  on_macos do
    depends_on macos: :ventura
  end

  def fetch
    system formula_opt_bin("go")/"go", "mod", "download"
  end

  def install
    ENV["GOPROXY"] = "off"
    system formula_opt_bin("go")/"go", "build", *std_go_args(output: "dotshift"), "./cmd/dotshift"

    if OS.mac?
      system "bash", "scripts/build-menubar.sh"
      prefix.install "build/Dotshift.app"
      bin.write_exec_script opt_prefix/"Dotshift.app/Contents/Resources/dotshift"
    else
      bin.install "dotshift"
    end
  end

  def caveats
    <<~EOS
      Create your configuration and sign in to Codex and Claude Code:
        mkdir -p ~/.config/dotshift
        dotshift init -config ~/.config/dotshift/dotshift.yaml
        codex login
        claude auth login

      Add your repositories and test commands to that configuration, then run:
        dotshift check -config ~/.config/dotshift/dotshift.yaml
        dotshift ui -config ~/.config/dotshift/dotshift.yaml

      On macOS, the menu app is included:
        dotshift menubar -config ~/.config/dotshift/dotshift.yaml

      Installation does not start a service or change existing configuration.
      See https://github.com/gregwhitaker/dotshift/blob/main/docs/homebrew.md
      for login startup, remote access, updates, and removal.
    EOS
  end

  test do
    assert_match "local coding and review", shell_output("#{bin}/dotshift help")
    config = testpath/"dotshift.yaml"
    system bin/"dotshift", "init", "-config", config
    assert_match "mode: subscription", config.read
    begin
      system bin/"dotshift", "ui", "-config", config, "-no-open", "-local-only"
      assert_match "Dotshift is running", shell_output("#{bin}/dotshift status -config #{config}")
    ensure
      system bin/"dotshift", "stop", "-config", config
    end
    if OS.mac?
      assert_path_exists prefix/"Dotshift.app/Contents/MacOS/DotshiftMenuBar"
      system "/usr/bin/codesign", "--verify", "--deep", "--strict", prefix/"Dotshift.app"
    end
  end
end
