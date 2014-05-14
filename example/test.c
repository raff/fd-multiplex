#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <sys/types.h>
#include <time.h> 
#include <multiplex.h>

#if defined(__MINGW32__) || defined(__MINGW64__)
#include <winsock2.h>

int inet_pton(int af, const char *src, void *dst) {
    int addr = inet_addr(src);
    if (addr == -1) {
    	return -1;
    }

    *((int *) dst) = addr;
    return 1;
}

void print_error(const char *msg) {
    fprintf(stderr, "%s: %d\n", msg, h_errno);
}
#else
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#define print_error perror
#endif

#define PORT 5000
#define MAX_CONN 10

int c = 0;

void * serve_connection(void *arg) {
    int conn = *((int *) arg);

    Multiplex * m = multiplex_new(conn);
    multiplex_enable_range(m, 0, 255, 256);

    int selected = 0;
    char * buffer;
    const int cc = ++c;

    while (selected != CHANNEL_CLOSED) {
	fputs("selecting...", stderr);
        selected = multiplex_select(m, 2000);
	fprintf(stderr, "selected %d\n", selected);
        if (selected >= 0) {
            buffer = multiplex_strdup(m, selected);
            printf("%d:[channel:%03d] %s\n", cc, selected, buffer);
            multiplex_clear(m, selected);
            free(buffer);

            char buffer[256];
            sprintf(buffer, "From server to channel %d.", selected);
            multiplex_send(m, selected, buffer, strlen(buffer));

            usleep(rand() % 1000000);
        }
    }

    fputs("closing connection", stderr);

    close(conn);
    return (void *)0;
}

int run_server(int port)
{
    int listenfd = 0, connfd = 0;
    struct sockaddr_in serv_addr; 

    char sendBuff[1025];
    time_t ticks; 

    /*
     * Listen to 0.0.0.0:port
     */
    listenfd = socket(AF_INET, SOCK_STREAM, 0);
    memset(&serv_addr, '0', sizeof(serv_addr));
    memset(sendBuff, '0', sizeof(sendBuff)); 

    serv_addr.sin_family = AF_INET;
    serv_addr.sin_addr.s_addr = htonl(INADDR_ANY);
    serv_addr.sin_port = htons(port); 

    if (bind(listenfd, (struct sockaddr*)&serv_addr, sizeof(serv_addr)) < 0) {
	print_error("bind");
	return 1;
    }

    if (listen(listenfd, MAX_CONN) < 0) {
	print_error("listen");
	return 1;
    }

    while(1)
    {
        /*
         * Accept new connection
         */
        connfd = accept(listenfd, (struct sockaddr*)NULL, NULL); 

	fprintf(stderr, "accepted %d\n", connfd);

        pthread_t thr;
        pthread_create(&thr, 0, &serve_connection, (void *) &connfd);
        pthread_detach(thr);
     }

     return 1;
}

int run_client(char *server, int port)
{
    int sockfd = 0, n = 0;
    char recvBuff[1024];
    struct sockaddr_in serv_addr; 

    memset(recvBuff, '0',sizeof(recvBuff));
    if((sockfd = socket(AF_INET, SOCK_STREAM, 0)) < 0)
    {
        print_error("create socket");
        return 1;
    } 

    memset(&serv_addr, '0', sizeof(serv_addr)); 

    serv_addr.sin_family = AF_INET;
    serv_addr.sin_port = htons(port); 

    if(inet_pton(AF_INET, server, &serv_addr.sin_addr)<=0)
    {
        print_error("inet_pton");
        return 1;
    } 

    if(connect(sockfd, (struct sockaddr *)&serv_addr, sizeof(serv_addr)) < 0)
    {
       print_error("connect");
       return 1;
    } 

    char buffer[256];
    int ch = 0;
    int i;
    Multiplex * m = multiplex_new(sockfd);
    multiplex_enable_range(m, 0, 255, 256);

    srand(time(0));

    for (i=0; i<100; i++) {
        ch = rand()%256;
        sprintf(buffer, "From client to channel %d.", ch);
        multiplex_send(m, ch, buffer, strlen(buffer));

        int selected = multiplex_select(m, 2000);
        if (selected >= 0) {
            char *resp = multiplex_strdup(m, selected);
            printf("Got [channel:%03d] %s\n", selected, resp);
            multiplex_clear(m, selected);
            free(resp);
        } else {
            usleep(rand()%1000000);
        }
    }

    close(sockfd);

    return 0;
}

int main(int argc, char **argv) {
   if (argc != 2) {
       printf("usage: %s [-s|-c]\n", argv[0]);
       return 1;
   } 

#if defined(__MINGW32__) || defined(__MINGW64__)
   WSADATA wsaData;
   WSAStartup(MAKEWORD(2,2), &wsaData);
#endif

   if (strcmp(argv[1], "-s") == 0) {
       return run_server(PORT);
   } else if (strcmp(argv[1], "-c") == 0) {
       return run_client("127.0.0.1", PORT);
   } else {
       printf("invalid option: %s\n", argv[1]);
       return 1;
   }

#if defined(__MINGW32__) || defined(__MINGW64__)
   WSACleanup();
#endif
}
