# Copyright 2026 Roxy Light
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

{ buildGoModule
, fzf
, nix-gitignore
, installShellFiles
, makeBinaryWrapper
, lib
}:

let
  version = "0.1.0";

  root = ./.;
  patterns = nix-gitignore.withGitignoreFile extraIgnores root;
  extraIgnores = [
    "*.nix"
    "flake.lock"
    "/.github/"
    ".vscode/"
    ".jj/"
    "result"
    "result-*"
  ];
  src = builtins.path {
    name = "deltat";
    path = root;
    filter = nix-gitignore.gitignoreFilterPure (_: _: true) patterns root;
  };
in
  buildGoModule {
    pname = "deltat";
    inherit version;

    inherit src;

    vendorHash = "sha256-zjoHb/kb/jmLvB2aPh9BtFY9uzV/0lB7S7C3DPWJRoI=";

    subPackages = ["."];
    goSum = builtins.readFile ./go.sum;
    ldflags = ["-s" "-w"];

    nativeBuildInputs = [
      installShellFiles
      makeBinaryWrapper
    ];

    postInstall = ''
      installShellCompletion --cmd deltat \
        --bash <($out/bin/deltat completion bash) \
        --fish <($out/bin/deltat completion fish) \
        --zsh <($out/bin/deltat completion zsh)

      wrapProgram $out/bin/deltat --set-default DELTAT_FZF ${fzf}/bin/fzf
    '';

    meta = {
      description = "Command-line time tracker";
      homepage = "https://github.com/zombiezen/deltat";
      license = lib.licenses.asl20;
    };
  }
