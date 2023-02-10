ALICE=alice@sour.is
ALICE_KEY=alice.key

BOB=bob@sour.is
BOB_KEY=bob.key

run:
	@rm -rf ./tmp
	@echo Alice starts by offering Bob to upgrade the connection.
	@echo
	go run ./cmd/ratchet --key $(ALICE_KEY) --state ./tmp offer $(BOB) | tee offer.msg

	@echo
	@echo "Bob acknowledges Alice's offer."
	@echo
	go run ./cmd/ratchet --key $(BOB_KEY) --state ./tmp --msg-file offer.msg recv | tee ack.msg
	
	@echo
	@echo "Alice evaluates Bob's acknowledgement."
	@echo
	go run ./cmd/ratchet --key $(ALICE_KEY) --state ./tmp recv --msg-file ack.msg 

	@echo
	@echo Alice sends message
	@echo
	go run ./cmd/ratchet --key $(ALICE_KEY) --state ./tmp send $(BOB) --msg hello | tee send1.msg

	@echo
	@echo Bob receives message. sends reply
	@echo
	go run ./cmd/ratchet --key $(BOB_KEY) --state ./tmp recv --msg-file send1.msg
	go run ./cmd/ratchet --key $(BOB_KEY)  --state ./tmp send $(ALICE) --msg yoyo | tee send2.msg

	@echo
	@echo Alice receives message. sends close
	@echo
	go run ./cmd/ratchet --key $(ALICE_KEY)  --state ./tmp recv --msg-file send2.msg	
	go run ./cmd/ratchet --key $(ALICE_KEY)  --state ./tmp close $(BOB) | tee close.msg

	@echo
	@echo Bob receives close.
	@echo
	go run ./cmd/ratchet --key $(BOB_KEY) --state ./tmp recv --msg-file close.msg


chat-bob:
	go run ./cmd/ratchet --key bob.key --state ./tmp --post chat 
chat-alice:
	go run ./cmd/ratchet --key alice.key --state ./tmp --post chat 

offer-bob:
	go run ./cmd/ratchet offer bob@sour.is --key alice.key --state ./tmp --post
close-alice:
	go run ./cmd/ratchet close alice@sour.is --key bob.key  --state ./tmp --post

send-bob:
	go run ./cmd/ratchet send bob@sour.is --key alice.key  --state ./tmp --post --msg $(MSG)
send-alice:
	go run ./cmd/ratchet send alice@sour.is --key bob.key  --state ./tmp --post --msg $(MSG)
