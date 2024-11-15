# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Mactop < Formula
  desc "Apple Silicon Monitor Top written in Go Lang"
  homepage "https://github.com/context-labs/mactop"
  version "0.2.1"
  depends_on :macos

  on_arm do
    url "https://github.com/context-labs/mactop/releases/download/v0.2.1/mactop_0.2.1_darwin_arm64.tar.gz"
    sha256 "fe6c20c96d7e92927b4e0808349ddb1285226499c682f62a2fd9e8c8ccd8652d"

    def install
      bin.install "mactop"
    end
  end

  def caveats
    <<~EOS
      mactop requires macOS 12+, and runs only on Apple Silicon.
    EOS
  end
end
