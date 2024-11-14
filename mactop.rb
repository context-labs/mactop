# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Mactop < Formula
  desc "Apple Silicon Monitor Top written in Go Lang"
  homepage "https://github.com/context-labs/mactop"
  version "0.2.0"
  depends_on :macos

  on_arm do
    url "https://github.com/context-labs/mactop/releases/download/v0.2.0/mactop_0.2.0_darwin_arm64.tar.gz"
    sha256 "62e339451e8336b1cc42cd53ab68f3fef4960ff517af47216fdb1ab0a4e4f31a"

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
