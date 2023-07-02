# SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
# SPDX-License-Identifier: CC0-1.0

GORUN?=go run .

ALICE=alice@sour.is
ALICE_KEY=alice.key

BOB=bob@sour.is
BOB_KEY=bob.key

build:
	@go version
	@go env
	@go build .
test: ## Run test suite
	@go version
	@go test -failfast -shuffle on -race -cover -coverprofile=coverage.out ./...
run-cover:
	@go version
	@rm -rf cover
	@mkdir -p cover
	go build -cover -o ./ratchet-cover .
	@GOCOVERDIR=cover GORUN=./ratchet-cover make simulate
	go test -cover ./... -test.gocoverdir=$(PWD)/cover/
	go tool covdata percent -i=cover/ -pkg go.salty.im/ratchet/...

run-ui:
	go build . && ./ratchet ui --key $(ALICE_KEY) --state ./tmp


simulate:
	@rm -rf ./tmp
	@chmod 400 *.key
	@echo Alice starts by offering Bob to upgrade the connection.
	@echo
	$(GORUN) --key $(ALICE_KEY) --state ./tmp offer $(BOB) | tee offer.msg

	@echo
	@echo "Bob acknowledges Alice's offer."
	@echo
	$(GORUN) --key $(BOB_KEY) --state ./tmp --msg-file offer.msg recv | tee ack.msg

	@echo
	@echo "Alice evaluates Bob's acknowledgement."
	@echo
	$(GORUN) --key $(ALICE_KEY) --state ./tmp recv --msg-file ack.msg

	@echo
	@echo Alice sends message
	@echo
	$(GORUN) --key $(ALICE_KEY) --state ./tmp send $(BOB) --msg hello | tee send1.msg

	@echo
	@echo Bob receives message. sends reply
	@echo
	$(GORUN) --key $(BOB_KEY) --state ./tmp recv --msg-file send1.msg
	$(GORUN) --key $(BOB_KEY)  --state ./tmp send $(ALICE) --msg yoyo | tee send2.msg

	@echo
	@echo Alice receives message. sends close
	@echo
	$(GORUN) --key $(ALICE_KEY)  --state ./tmp recv --msg-file send2.msg
	$(GORUN) --key $(ALICE_KEY)  --state ./tmp close $(BOB) | tee close.msg

	@echo
	@echo Bob receives close.
	@echo
	$(GORUN) --key $(BOB_KEY) --state ./tmp recv --msg-file close.msg


chat-bob:
	go build .; ./ratchet --key bob.key --state ./tmp --post chat alice@sour.is
chat-alice:
	go build .; ./ratchet --key alice.key --state ./tmp --post chat bob@sour.is

offer-bob:
	$(GORUN) offer bob@sour.is --key alice.key --state ./tmp --post
close-alice:
	$(GORUN) close alice@sour.is --key bob.key  --state ./tmp --post

send-bob:
	$(GORUN) send bob@sour.is --key alice.key  --state ./tmp --post --msg $(MSG)
send-alice:
	$(GORUN) send alice@sour.is --key bob.key  --state ./tmp --post --msg $(MSG)

