#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <stdio.h>
#include <stdlib.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <sys/types.h>
#include <time.h> 
#include <multiplex.h>

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
        selected = multiplex_select(m, 2000);
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

    bind(listenfd, (struct sockaddr*)&serv_addr, sizeof(serv_addr)); 
    listen(listenfd, MAX_CONN); 

    while(1)
    {
        /*
         * Accept new connection
         */
        connfd = accept(listenfd, (struct sockaddr*)NULL, NULL); 

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
        printf("\n Error : Could not create socket \n");
        return 1;
    } 

    memset(&serv_addr, '0', sizeof(serv_addr)); 

    serv_addr.sin_family = AF_INET;
    serv_addr.sin_port = htons(port); 

    if(inet_pton(AF_INET, server, &serv_addr.sin_addr)<=0)
    {
        printf("\n inet_pton error occured\n");
        return 1;
    } 

    if( connect(sockfd, (struct sockaddr *)&serv_addr, sizeof(serv_addr)) < 0)
    {
       printf("\n Error : Connect Failed \n");
       return 1;
    } 

    char buffer[256];
    int ch = 0;
    Multiplex * m = multiplex_new(sockfd);
    multiplex_enable_range(m, 0, 255, 256);

    srand(time(0));

    for (int i=0; i<100; i++) {
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

   if (strcmp(argv[1], "-s") == 0) {
       return run_server(PORT);
   } else if (strcmp(argv[1], "-c") == 0) {
       return run_client("127.0.0.1", PORT);
   } else {
       printf("invalid option: %s\n", argv[1]);
       return 1;
   }
}
