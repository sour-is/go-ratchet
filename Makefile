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
	go run ./cmd/ratchet --key $(BOB_KEY) --state ./tmp --msg offer.msg recv | tee ack.msg
	
	@echo
	@echo "Alice evaluates Bob's acknowledgement."
	@echo
	@cat ack.msg | go run ./cmd/ratchet --key $(ALICE_KEY) --state ./tmp recv  

	@echo
	@echo Alice sends message
	@echo
	echo hello | go run ./cmd/ratchet --key $(ALICE_KEY) --state ./tmp send $(BOB) | tee send1.msg

	@echo
	@echo Bob receives message. sends reply
	@echo
	@cat send1.msg | go run ./cmd/ratchet --key $(BOB_KEY) --state ./tmp recv 
	@echo yoyo | go run ./cmd/ratchet --key $(BOB_KEY)  --state ./tmp send $(ALICE) | tee send2.msg

	@echo
	@echo Alice receives message. sends close
	@echo
	@cat send2.msg | go run ./cmd/ratchet --key $(ALICE_KEY)  --state ./tmp recv	
	go run ./cmd/ratchet --key $(ALICE_KEY)  --state ./tmp close $(BOB)| tee close.msg

	@echo
	@echo Bob receives close.
	@echo
	@cat close.msg | go run ./cmd/ratchet --key $(BOB_KEY) --state ./tmp recv


chat-bob:
	go run ./cmd/ratchet chat --me bob@sour.is --key bob.key --state ./tmp --post
chat-alice:
	go run ./cmd/ratchet chat --me alice@sour.is --key alice.key --state ./tmp --post

offer-bob:
	go run ./cmd/ratchet offer --me alice@sour.is --them bob@sour.is --key alice.key --state ./tmp --post
close-alice:
	go run ./cmd/ratchet offer --me bob@sour.is --them alice@sour.is --key bob.key  --state ./tmp --post

send-bob:
	 echo $(MSG) | go run ./cmd/ratchet send --me alice@sour.is --them bob@sour.is --key alice.key  --state ./tmp --post
send-alice:
	 echo $(MSG) | go run ./cmd/ratchet send --me bob@sour.is --them alice@sour.is --key bob.key  --state ./tmp --post
