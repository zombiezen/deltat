#!/bin/sh
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

# Use the following VSCode setting:
# "go.alternateTools": {
#   "go": "${workspaceFolder}/tools/go.sh"
# },

export DIRENV_LOG_FORMAT=''
if [ $# -gt 1 ] && [ "$1" = env ]; then
  # VSCode does not like having stderr output on go env.
  exec direnv exec "$(dirname "$0")/.." go "$@" 2> /dev/null
fi
exec direnv exec "$(dirname "$0")/.." go "$@"
