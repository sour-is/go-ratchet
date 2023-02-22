ALICE=alice@sour.is
ALICE_KEY=alice.key

BOB=bob@sour.is
BOB_KEY=bob.key

ui:
	go build . && ./ratchet ui --key $(ALICE_KEY) --state ./tmp



simulate:
	@rm -rf ./tmp
	@chmod 400 *.key
	@echo Alice starts by offering Bob to upgrade the connection.
	@echo
	go run . --key $(ALICE_KEY) --state ./tmp offer $(BOB) | tee offer.msg

	@echo
	@echo "Bob acknowledges Alice's offer."
	@echo
	go run . --key $(BOB_KEY) --state ./tmp --msg-file offer.msg recv | tee ack.msg

	@echo
	@echo "Alice evaluates Bob's acknowledgement."
	@echo
	go run . --key $(ALICE_KEY) --state ./tmp recv --msg-file ack.msg

	@echo
	@echo Alice sends message
	@echo
	go run . --key $(ALICE_KEY) --state ./tmp send $(BOB) --msg hello | tee send1.msg

	@echo
	@echo Bob receives message. sends reply
	@echo
	go run . --key $(BOB_KEY) --state ./tmp recv --msg-file send1.msg
	go run . --key $(BOB_KEY)  --state ./tmp send $(ALICE) --msg yoyo | tee send2.msg

	@echo
	@echo Alice receives message. sends close
	@echo
	go run . --key $(ALICE_KEY)  --state ./tmp recv --msg-file send2.msg
	go run . --key $(ALICE_KEY)  --state ./tmp close $(BOB) | tee close.msg

	@echo
	@echo Bob receives close.
	@echo
	go run . --key $(BOB_KEY) --state ./tmp recv --msg-file close.msg


chat-bob:
	go build .; ./ratchet --key bob.key --state ./tmp --post chat alice@sour.is
chat-alice:
	go build .; ./ratchet --key alice.key --state ./tmp --post chat

offer-bob:
	go run . offer bob@sour.is --key alice.key --state ./tmp --post
close-alice:
	go run . close alice@sour.is --key bob.key  --state ./tmp --post

send-bob:
	go run . send bob@sour.is --key alice.key  --state ./tmp --post --msg $(MSG)
send-alice:
	go run . send alice@sour.is --key bob.key  --state ./tmp --post --msg $(MSG)

