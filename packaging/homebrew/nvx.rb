# Formula for Homebrew, published to fstubner/homebrew-tap.
#
# This file IS the shipped formula, minus its digests and this header.
# `scripts/release/publish-homebrew.sh` runs from publish.yml on every
# release. It downloads each asset, hashes the bytes, checks the result
# against the published .sha256 sidecar, then regenerates the tap's
# Formula/nvx.rb from everything below the `class` line here, with the
# digests and `version` substituted in.
#
# So an edit to the formula body below reaches users on the next release,
# and an edit made directly in the tap does not survive one.
#
# The digests here are @@...@@ placeholders on purpose. Real-looking 64-hex
# values left behind by whichever release last touched the file read as the
# shipped manifest, and would install a binary that fails Homebrew's
# integrity check if anyone copied them into the tap. A placeholder is
# unmistakably unfilled.
#
# Editing it by hand is only needed if you are bootstrapping the tap or the
# publish job is broken. In that case take each digest from the `.sha256`
# asset on the release page.
#
# Usage:
#   brew tap fstubner/tap
#   brew install nvx
class Nvx < Formula
  desc "A Node.js and Bun version manager that runs every install inside an OS sandbox"
  homepage "https://nvx.run"
  version "0.6.0"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/fstubner/nvx/releases/download/v#{version}/nvx-darwin-arm64"
      sha256 "@@SHA256_DARWIN_ARM64@@"
    end
    on_intel do
      url "https://github.com/fstubner/nvx/releases/download/v#{version}/nvx-darwin-amd64"
      sha256 "@@SHA256_DARWIN_AMD64@@"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/fstubner/nvx/releases/download/v#{version}/nvx-linux-arm64"
      sha256 "@@SHA256_LINUX_ARM64@@"
    end
    on_intel do
      url "https://github.com/fstubner/nvx/releases/download/v#{version}/nvx-linux-amd64"
      sha256 "@@SHA256_LINUX_AMD64@@"
    end
  end

  def install
    # The asset is the bare binary, so rename it. `bin.install` marks it
    # executable. There is no completions or man generator to call here.
    # nvx's only self-describing commands are `nvx version` and `nvx help`.
    asset_name = if OS.mac?
      Hardware::CPU.arm? ? "nvx-darwin-arm64" : "nvx-darwin-amd64"
    else
      Hardware::CPU.arm? ? "nvx-linux-arm64" : "nvx-linux-amd64"
    end
    bin.install asset_name => "nvx"
  end

  test do
    assert_match "nvx version #{version}", shell_output("#{bin}/nvx --version")
  end
end
